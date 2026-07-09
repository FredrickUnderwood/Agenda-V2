// Package promclient is a thin control-plane client for Prometheus's HTTP
// query API — used by the alert rule engine to evaluate a rule's PromQL
// expression. Kept separate from nodeproxy/gatewayclient (one client package
// per external HTTP peer) since Prometheus is a different kind of peer:
// operator-deployed infrastructure, not a platform component.
package promclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
)

// queryTimeout bounds one instant query. Rule evaluation is a background
// job, so it can afford more slack than a live user-facing request.
const queryTimeout = 8 * time.Second

// VectorSample is one series of an instant query's vector result. Value is
// Prometheus's wire shape: [unix_timestamp_seconds, "string_value"].
type VectorSample struct {
	Metric map[string]string `json:"metric"`
	Value  [2]any            `json:"value"`
}

// QueryResult is the "data" object of Prometheus's query API response.
type QueryResult struct {
	ResultType string         `json:"resultType"`
	Result     []VectorSample `json:"result"`
}

type queryResponse struct {
	Status    string      `json:"status"`
	Data      QueryResult `json:"data"`
	ErrorType string      `json:"errorType"`
	Error     string      `json:"error"`
}

// Query runs an instant query against Prometheus's HTTP API
// (GET <baseURL>/api/v1/query) and parses its {"status","data":{...}}
// envelope. A non-"success" status or a transport/HTTP error is returned as
// an error — callers should treat that as "evaluation failed", distinct from
// a successful query that simply returned zero series.
func Query(ctx context.Context, baseURL, promql string, at time.Time) (*QueryResult, error) {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		return nil, errors.New("prometheus base url is empty")
	}
	u, err := url.Parse(base + "/api/v1/query")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("query", promql)
	q.Set("time", strconv.FormatInt(at.Unix(), 10))
	u.RawQuery = q.Encode()

	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: queryTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("prometheus query failed: " + resp.Status + ": " + strings.TrimSpace(string(body)))
	}

	var out queryResponse
	if err := sonic.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if out.Status != "success" {
		msg := out.Error
		if msg == "" {
			msg = "unknown prometheus error"
		}
		return nil, errors.New("prometheus query error (" + out.ErrorType + "): " + msg)
	}
	return &out.Data, nil
}
