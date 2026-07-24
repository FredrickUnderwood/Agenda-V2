package gatewayclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bytedance/sonic"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
)

const serviceTokenHeader = "X-Service-Token"

type Client struct {
	baseURL      string
	serviceToken string
	httpClient   *http.Client
}

func NewClient(cfg config.GatewayConfig) *Client {
	timeout := cfg.Timeout.Duration
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		baseURL:      strings.TrimRight(cfg.BaseURL, "/"),
		serviceToken: cfg.ServiceToken,
		httpClient:   &http.Client{Timeout: timeout},
	}
}

func (c *Client) UpsertRoute(ctx context.Context, routeKey string, req contract.UpsertRouteRequest) error {
	if c == nil || c.baseURL == "" {
		return errors.New("gateway base_url is required")
	}
	if c.serviceToken == "" {
		return errors.New("gateway service_token is required")
	}
	if routeKey == "" {
		return errors.New("gateway route key is required")
	}
	raw, err := sonic.Marshal(req)
	if err != nil {
		return err
	}
	endpoint := c.baseURL + "/-/routes/" + url.PathEscape(routeKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(serviceTokenHeader, c.serviceToken)
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return errors.New("gateway upsert route failed: " + resp.Status + ": " + strings.TrimSpace(string(body)))
	}
	return nil
}

// PutTLSConfig pushes the edge-TLS ACME/DNS credentials (sourced from the
// control plane's Settings) to the gateway admin endpoint. The gateway holds
// them in memory only and hot-applies them; a gateway that is not the TLS edge
// accepts the call as a no-op.
func (c *Client) PutTLSConfig(ctx context.Context, req contract.UpdateTLSConfigRequest) error {
	if c == nil || c.baseURL == "" {
		return errors.New("gateway base_url is required")
	}
	if c.serviceToken == "" {
		return errors.New("gateway service_token is required")
	}
	raw, err := sonic.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/-/tls", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(serviceTokenHeader, c.serviceToken)
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return errors.New("gateway put tls config failed: " + resp.Status + ": " + strings.TrimSpace(string(body)))
	}
	return nil
}
