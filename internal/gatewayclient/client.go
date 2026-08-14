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

// WebSocketStat is one row of GET /-/ws/connections: how many tunnels a given
// (route, instance) pair currently holds on the gateway.
type WebSocketStat struct {
	RouteKey string `json:"route_key"`
	Instance string `json:"instance"`
	Active   int    `json:"active"`
}

type webSocketConnectionsResponse struct {
	Data   []WebSocketStat `json:"data"`
	Active int             `json:"active"`
}

// ActiveWebSocketConnections returns how many WebSocket tunnels the gateway
// still holds for an instance, optionally narrowed to one route.
//
// Used by the decommission pipeline: after the route has been pointed away from
// an instance, tunnels opened before that are still attached to it, and the
// only way to avoid cutting them is to ask the gateway whether any remain.
func (c *Client) ActiveWebSocketConnections(ctx context.Context, routeKey, instance string) (int, error) {
	if c == nil || c.baseURL == "" {
		return 0, errors.New("gateway base_url is required")
	}
	if c.serviceToken == "" {
		return 0, errors.New("gateway service_token is required")
	}
	endpoint := c.baseURL + "/-/ws/connections"
	query := url.Values{}
	if routeKey != "" {
		query.Set("route_key", routeKey)
	}
	if instance != "" {
		query.Set("instance", instance)
	}
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	httpReq.Header.Set(serviceTokenHeader, c.serviceToken)
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, errors.New("gateway list websocket connections failed: " + resp.Status + ": " + strings.TrimSpace(string(body)))
	}
	var out webSocketConnectionsResponse
	if err := sonic.Unmarshal(body, &out); err != nil {
		return 0, err
	}
	return out.Active, nil
}
