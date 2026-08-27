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

// ExecuteRedisCommand asks the node at agentBaseURL to run one read-only Redis
// command against a server on its own machine and relay the reply.
//
// It keeps ExecuteQuery's error split, and the caller must honor it the same
// way: a returned error means the *node* could not be reached, while a returned
// response with Error set means the node ran and *Redis* refused or failed.
func ExecuteRedisCommand(ctx context.Context, agentBaseURL, agentToken string, req contract.NodeRedisCommandRequest) (*contract.NodeRedisCommandResponse, error) {
	base := strings.TrimRight(agentBaseURL, "/")
	if base == "" {
		return nil, errors.New("agent_base_url is empty; cannot execute command")
	}
	raw, err := sonic.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/redis/command", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(contract.HeaderNodeToken, agentToken)

	// Headroom over the command timeout the node applies locally, so its own
	// bounded command returns before this call gives up.
	timeout := time.Duration(req.TimeoutMS)*time.Millisecond + 15*time.Second
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(contract.NodeDBMaxBytes)*2))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("execute redis command failed: " + resp.Status + ": " + strings.TrimSpace(string(body)))
	}

	var out contract.NodeRedisCommandResponse
	if err := sonic.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
