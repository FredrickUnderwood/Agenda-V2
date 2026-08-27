package nodeproxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
)

// ExecuteQuery asks the node at agentBaseURL to run one read-only statement
// against a database on its own machine and relay the result set.
//
// It mirrors Probe's error split, and the caller must honor it: a returned
// error means the *node* could not be reached (agent down, network, bad
// token), while a returned response with Error set means the node ran and the
// *database* refused or failed. Collapsing the two would report a stopped
// database as a broken agent.
func ExecuteQuery(ctx context.Context, agentBaseURL, agentToken string, req contract.NodeDBQueryRequest) (*contract.NodeDBQueryResponse, error) {
	base := strings.TrimRight(agentBaseURL, "/")
	if base == "" {
		return nil, errors.New("agent_base_url is empty; cannot execute query")
	}
	raw, err := sonic.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/db/query", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(contract.HeaderNodeToken, agentToken)

	// Give the node headroom over the statement timeout it applies locally, so
	// its own bounded query returns before this call gives up — the same
	// reasoning as Probe's timeout.
	timeout := time.Duration(req.TimeoutMS)*time.Millisecond + 15*time.Second
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Bounded by the node's own max_bytes cap plus JSON overhead.
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(contract.NodeDBMaxBytes)*2))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("execute query failed: " + resp.Status + ": " + strings.TrimSpace(string(body)))
	}

	var out contract.NodeDBQueryResponse
	if err := sonic.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
