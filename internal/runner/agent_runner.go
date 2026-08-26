package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/filestore"
)

const defaultPollInterval = 2 * time.Second

// agentRunner implements Runner by dispatching a job to an agenda-node and
// polling until it reaches a terminal state. To the pipeline layer this looks
// exactly like a blocking sshRunner/localRunner call — the dispatch+poll dance
// is hidden inside run. The command runs on the node independently of any single
// HTTP request, so a transient poll failure does not fail the step; only ctx
// expiry (the deploy's overall timeout) or the node losing the job does.
type agentRunner struct {
	machine *config.MachineConfig
	client  *http.Client
}

func newAgentRunner(machine *config.MachineConfig) *agentRunner {
	return &agentRunner{
		machine: machine,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *agentRunner) RunCmd(ctx context.Context, dir, name string, args []string, buf *bytes.Buffer) error {
	return a.run(ctx, contract.NodeJobRequest{Mode: contract.NodeJobModeCmd, Dir: dir, Name: name, Args: args}, buf)
}

func (a *agentRunner) RunCmdEnv(ctx context.Context, dir string, env []string, name string, args []string, buf *bytes.Buffer) error {
	return a.run(ctx, contract.NodeJobRequest{Mode: contract.NodeJobModeCmd, Dir: dir, Env: env, Name: name, Args: args}, buf)
}

func (a *agentRunner) RunShell(ctx context.Context, dir, shellCmd string, buf *bytes.Buffer) error {
	return a.run(ctx, contract.NodeJobRequest{Mode: contract.NodeJobModeShell, Dir: dir, Shell: shellCmd}, buf)
}

func (a *agentRunner) run(ctx context.Context, req contract.NodeJobRequest, buf *bytes.Buffer) error {
	req.JobID = uuid.NewString()
	if err := a.postJSON(ctx, "/v1/jobs", req); err != nil {
		return err
	}
	// Best-effort reclaim: whether we finish or the caller's ctx expires, ask the
	// node to drop the job so a command nobody is waiting on doesn't linger. Use
	// a detached ctx so cancellation itself doesn't abort the cleanup call.
	defer func() {
		_ = a.deleteJob(context.WithoutCancel(ctx), req.JobID)
	}()

	ticker := time.NewTicker(a.pollInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			st, code, err := a.getJob(ctx, req.JobID)
			if err != nil {
				// A single poll failure (network blip) is not a job failure —
				// retry next tick. A 404 means the node lost the job (restart);
				// that is a real, unrecoverable loss for this step.
				if code == http.StatusNotFound {
					return errors.New("agent lost job (node restarted?); step failed")
				}
				continue
			}
			switch st.Status {
			case "", "running":
				// keep polling
			case "success":
				buf.WriteString(st.Output)
				return nil
			case "failed":
				buf.WriteString(st.Output)
				if st.Error != "" {
					return errors.New(st.Error)
				}
				return errors.New("agent job failed")
			}
		}
	}
}

func (a *agentRunner) pollInterval() time.Duration {
	if a.machine != nil && a.machine.AgentPollInterval > 0 {
		return a.machine.AgentPollInterval
	}
	return defaultPollInterval
}

func (a *agentRunner) baseURL() string {
	return strings.TrimRight(a.machine.AgentBaseURL, "/")
}

func (a *agentRunner) postJSON(ctx context.Context, path string, body any) error {
	raw, err := sonic.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL()+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(contract.HeaderNodeToken, a.machine.AgentToken)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return errors.New("agent " + path + " failed: " + resp.Status + ": " + strings.TrimSpace(string(msg)))
	}
	return nil
}

// getJob returns the job status, the HTTP status code (for 404 detection), and
// an error on transport/decoding failure.
func (a *agentRunner) getJob(ctx context.Context, jobID string) (contract.NodeJobStatus, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL()+"/v1/jobs/"+jobID, nil)
	if err != nil {
		return contract.NodeJobStatus{}, 0, err
	}
	req.Header.Set(contract.HeaderNodeToken, a.machine.AgentToken)
	resp, err := a.client.Do(req)
	if err != nil {
		return contract.NodeJobStatus{}, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return contract.NodeJobStatus{}, resp.StatusCode, errors.New("non-200 from agent: " + resp.Status)
	}
	var st contract.NodeJobStatus
	if err := sonic.ConfigDefault.NewDecoder(resp.Body).Decode(&st); err != nil {
		return contract.NodeJobStatus{}, resp.StatusCode, err
	}
	return st, resp.StatusCode, nil
}

func (a *agentRunner) deleteJob(ctx context.Context, jobID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, a.baseURL()+"/v1/jobs/"+jobID, nil)
	if err != nil {
		return err
	}
	req.Header.Set(contract.HeaderNodeToken, a.machine.AgentToken)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// agentUploadClient carries no timeout of its own because an upload is not
// bounded the way a job dispatch is: a large file over a slow link legitimately
// takes minutes, and the 30s dispatch timeout would abort it mid-transfer. The
// request context (the caller's deadline) is what bounds it.
//
// agentStatClient does have one. A stat is a small, quick call, and some of its
// callers — a console verify button — run under a request context with no
// deadline at all, where a node that accepts the connection and then goes quiet
// would hang the handler indefinitely.
var (
	agentUploadClient = &http.Client{}
	agentStatClient   = &http.Client{Timeout: 30 * time.Second}
)

func (a *agentRunner) PutFile(ctx context.Context, path string, src io.Reader, mode string, overwrite bool) (contract.FileStat, error) {
	u, err := url.Parse(a.baseURL() + "/v1/files")
	if err != nil {
		return contract.FileStat{}, err
	}
	q := u.Query()
	q.Set(contract.NodeFileQueryPath, path)
	if mode != "" {
		q.Set(contract.NodeFileQueryMode, mode)
	}
	if overwrite {
		q.Set(contract.NodeFileQueryOverwrite, "true")
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), src)
	if err != nil {
		return contract.FileStat{}, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(contract.HeaderNodeToken, a.machine.AgentToken)
	return doFileRequest(agentUploadClient, req, "put file "+path)
}

func (a *agentRunner) StatFile(ctx context.Context, path string) (contract.FileStat, error) {
	u, err := url.Parse(a.baseURL() + "/v1/files/stat")
	if err != nil {
		return contract.FileStat{}, err
	}
	q := u.Query()
	q.Set(contract.NodeFileQueryPath, path)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return contract.FileStat{}, err
	}
	req.Header.Set(contract.HeaderNodeToken, a.machine.AgentToken)
	return doFileRequest(agentStatClient, req, "stat file "+path)
}

// doFileRequest performs a node file request and maps the node's status codes
// back onto the filestore sentinels, so a caller sees the same error whichever
// runner backend it happens to be talking to.
func doFileRequest(client *http.Client, req *http.Request, what string) (contract.FileStat, error) {
	resp, err := client.Do(req)
	if err != nil {
		return contract.FileStat{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if err != nil {
		return contract.FileStat{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(body))
		switch resp.StatusCode {
		case http.StatusConflict:
			return contract.FileStat{}, filestore.ErrExists
		case http.StatusRequestEntityTooLarge:
			return contract.FileStat{}, filestore.ErrTooLarge
		case http.StatusForbidden:
			return contract.FileStat{}, fmt.Errorf("%w: %s", filestore.ErrOutsideRoots, msg)
		case http.StatusNotFound:
			// Every other node route predates file transfer, so a 404 here is
			// almost always an agenda-node too old to have the endpoint at all
			// — a fact worth stating instead of relaying "404 page not found".
			return contract.FileStat{}, errors.New(what + " failed: this machine's agenda-node does not support file transfer; upgrade it")
		}
		return contract.FileStat{}, errors.New(what + " failed: " + resp.Status + ": " + msg)
	}
	var out contract.FileStat
	if err := sonic.Unmarshal(body, &out); err != nil {
		return contract.FileStat{}, err
	}
	return out, nil
}
