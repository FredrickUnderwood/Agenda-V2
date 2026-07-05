// Package nodeproxy is a thin control-plane client for agenda-node's proxy
// registration API (PUT /v1/proxy/:instance). It is kept separate from the
// gateway client (which talks to agenda-gateway) to avoid conflating the two.
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

// RegisterProxyTarget tells the node at agentBaseURL that instanceName currently
// listens on the given local port, so the node's reverse proxy forwards
// /i/<instance> there. Called before syncing a gateway route in agent mode.
func RegisterProxyTarget(ctx context.Context, agentBaseURL, agentToken, instanceName string, port int) error {
	base := strings.TrimRight(agentBaseURL, "/")
	if base == "" {
		return errors.New("agent_base_url is empty; cannot register proxy target")
	}
	raw, err := sonic.Marshal(contract.NodeProxyRegisterRequest{Port: port})
	if err != nil {
		return err
	}
	endpoint := base + "/v1/proxy/" + instanceName
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(contract.HeaderNodeToken, agentToken)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return errors.New("register proxy target failed: " + resp.Status + ": " + strings.TrimSpace(string(msg)))
	}
	return nil
}
