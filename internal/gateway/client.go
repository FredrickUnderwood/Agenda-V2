package gateway

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

	"github.com/agenda-v2/config"
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

type UpsertRouteRequest struct {
	ApplicationID      int64          `json:"application_id"`
	ServiceName        string         `json:"service_name"`
	Env                string         `json:"env"`
	Host               string         `json:"host"`
	PathPrefix         string         `json:"path_prefix"`
	StripPrefix        bool           `json:"strip_prefix"`
	ReleaseID          string         `json:"release_id"`
	Status             string         `json:"status"`
	InstanceSelectMode string         `json:"instance_select_mode,omitempty"`
	InstanceHeader     string         `json:"instance_header,omitempty"`
	Operator           string         `json:"operator"`
	Reason             string         `json:"reason"`
	Backends           []BackendEntry `json:"backends"`
}

type BackendEntry struct {
	TargetKey    string `json:"target_key"`
	InstanceName string `json:"instance_name,omitempty"`
	URL          string `json:"url"`
	Weight       int    `json:"weight"`
	Enabled      bool   `json:"enabled"`
	Healthy      bool   `json:"healthy"`
}

func (c *Client) UpsertRoute(ctx context.Context, routeKey string, req UpsertRouteRequest) error {
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
