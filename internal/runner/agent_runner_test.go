package runner_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bytedance/sonic"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/node"
	"github.com/FredrickUnderwood/agenda-v2/internal/runner"
)

// newTestNode spins up a real agenda-node management server backed by an
// in-process JobStore, so the agentRunner exercises the true dispatch+poll wire
// path end to end.
func newTestNode(t *testing.T, token string) (*config.MachineConfig, func()) {
	t.Helper()
	jobs := node.NewJobStore(65536, time.Hour)
	srv := node.NewServer(token, jobs, node.NewProxyRegistry(), "")
	ts := httptest.NewServer(srv.Handler())
	mc := &config.MachineConfig{
		Mode:              "agent",
		AgentBaseURL:      ts.URL,
		AgentToken:        token,
		AgentPollInterval: 10 * time.Millisecond,
	}
	return mc, ts.Close
}

func TestAgentRunnerRunShellSuccess(t *testing.T) {
	mc, closeFn := newTestNode(t, "tok")
	defer closeFn()

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runner.New(mc).RunShell(ctx, "", "echo agent-hello", &buf); err != nil {
		t.Fatalf("RunShell: %v", err)
	}
	if !strings.Contains(buf.String(), "agent-hello") {
		t.Fatalf("output = %q, want it to contain agent-hello", buf.String())
	}
}

func TestAgentRunnerRunCmdFailurePropagates(t *testing.T) {
	mc, closeFn := newTestNode(t, "tok")
	defer closeFn()

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// `false` exits non-zero → the job fails and the runner returns an error.
	err := runner.New(mc).RunCmd(ctx, "", "false", nil, &buf)
	if err == nil {
		t.Fatal("expected error from failing command")
	}
}

func TestAgentRunnerBadTokenRejected(t *testing.T) {
	mc, closeFn := newTestNode(t, "right-token")
	defer closeFn()
	mc.AgentToken = "wrong-token"

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runner.New(mc).RunShell(ctx, "", "echo x", &buf); err == nil {
		t.Fatal("expected dispatch to be rejected with a bad token")
	}
}

func TestAgentRunnerContextCancel(t *testing.T) {
	mc, closeFn := newTestNode(t, "tok")
	defer closeFn()

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	// A command that outlives the ctx → run returns the ctx error, not success.
	err := runner.New(mc).RunShell(ctx, "", "sleep 5", &buf)
	if err == nil {
		t.Fatal("expected context deadline error")
	}
}

func TestAgentRunnerTimeoutRecoversOutputBeforeFirstPoll(t *testing.T) {
	mc, closeFn := newTestNode(t, "tok")
	defer closeFn()
	mc.AgentPollInterval = time.Hour
	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := runner.New(mc).RunShell(ctx, "", "printf 'pulling base image\\n'; sleep 30", &buf)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunShell error = %v", err)
	}
	if buf.String() != "pulling base image\n" {
		t.Fatalf("timeout output = %q", buf.String())
	}
}

func TestAgentRunnerSnapshotsAreNotDuplicated(t *testing.T) {
	mc, closeFn := newTestNode(t, "tok")
	defer closeFn()
	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := runner.New(mc).RunShell(ctx, "", "printf 'first\\n'; sleep 0.1; printf 'last\\n' >&2", &buf)
	if err != nil || buf.String() != "first\nlast\n" {
		t.Fatalf("RunShell = %v, output = %q", err, buf.String())
	}
}

func TestAgentRunnerKeepsLastSnapshotWhenFinalFetchFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var gets atomic.Int32
	var deleted atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
		case http.MethodGet:
			if gets.Add(1) == 1 {
				raw, _ := sonic.Marshal(contract.NodeJobStatus{Status: "running", Output: "last available progress\n"})
				w.Write(raw)
			} else {
				// The second poll can happen only after the first snapshot was
				// processed. Make the node unavailable as the context expires.
				cancel()
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		case http.MethodDelete:
			deleted.Store(true)
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer ts.Close()
	mc := &config.MachineConfig{Mode: "agent", AgentBaseURL: ts.URL, AgentPollInterval: time.Millisecond}
	var buf bytes.Buffer
	io.WriteString(&buf, "previous command\n")
	err := runner.New(mc).RunShell(ctx, "", "unused", &buf)
	if !errors.Is(err, context.Canceled) || buf.String() != "previous command\nlast available progress\n" {
		t.Fatalf("error=%v, output=%q", err, buf.String())
	}
	if !deleted.Load() || gets.Load() < 3 {
		t.Fatal("expected a final snapshot attempt before job cleanup")
	}
}
