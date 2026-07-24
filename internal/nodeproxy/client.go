// Package nodeproxy is a thin control-plane client for agenda-node's
// management API (proxy registration, log tailing). It is kept separate from
// the gateway client (which talks to agenda-gateway) to avoid conflating the
// two.
package nodeproxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
)

// RegisterProxyTarget tells the node at agentBaseURL that the instance
// identified by proxyKey (the app-scoped key from ProxyKey — never a bare
// instance name, which collides across apps sharing a machine) currently
// listens on the given local port, so the node's reverse proxy forwards
// /i/<proxyKey> there. Called before syncing a gateway route in agent mode,
// and by the proxy resync loop.
func RegisterProxyTarget(ctx context.Context, agentBaseURL, agentToken, proxyKey string, port int) error {
	base := strings.TrimRight(agentBaseURL, "/")
	if base == "" {
		return errors.New("agent_base_url is empty; cannot register proxy target")
	}
	raw, err := sonic.Marshal(contract.NodeProxyRegisterRequest{Port: port})
	if err != nil {
		return err
	}
	endpoint := base + "/v1/proxy/" + proxyKey
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

// FetchLogs asks the node at agentBaseURL for the tail of every log file
// matching app/instance under dir — the absolute host-side log directory the
// control plane resolves the same way it resolves a deploy target's working
// directory (see git.ResolveLocalPath), NOT contract.AgendaContainerLogDir
// (that's only the path a deployed app's own container sees). service and
// tail are optional (service == "" means "all services"; tail <= 0 leaves
// the node's own default).
func FetchLogs(ctx context.Context, agentBaseURL, agentToken, app, instance, dir, service string, tail int) (*contract.NodeLogsResponse, error) {
	base := strings.TrimRight(agentBaseURL, "/")
	if base == "" {
		return nil, errors.New("agent_base_url is empty; cannot fetch logs")
	}
	u, err := url.Parse(base + "/v1/logs/" + url.PathEscape(app) + "/" + url.PathEscape(instance))
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("dir", dir)
	if service != "" {
		q.Set("service", service)
	}
	if tail > 0 {
		q.Set("tail", strconv.Itoa(tail))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(contract.HeaderNodeToken, agentToken)
	client := &http.Client{Timeout: 10 * time.Second}
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
		return nil, errors.New("fetch logs failed: " + resp.Status + ": " + strings.TrimSpace(string(body)))
	}
	var out contract.NodeLogsResponse
	if err := sonic.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FetchMetrics asks the node at agentBaseURL for app/instance's current
// Prometheus exposition-format text, scraped from its local metricsPort.
// path == "" leaves the node's own default (contract.DefaultMetricsPath);
// scheme == "" leaves the node's default (http). Returns the raw body and the
// node's own Content-Type verbatim — unlike FetchLogs, there is no JSON
// envelope to unmarshal: the body must reach Prometheus byte-for-byte.
func FetchMetrics(ctx context.Context, agentBaseURL, agentToken, app, instance string, metricsPort int, path, scheme string) (body []byte, contentType string, err error) {
	base := strings.TrimRight(agentBaseURL, "/")
	if base == "" {
		return nil, "", errors.New("agent_base_url is empty; cannot fetch metrics")
	}
	u, err := url.Parse(base + "/v1/metrics/" + url.PathEscape(app) + "/" + url.PathEscape(instance))
	if err != nil {
		return nil, "", err
	}
	q := u.Query()
	q.Set(contract.NodeMetricsQueryPort, strconv.Itoa(metricsPort))
	if path != "" {
		q.Set(contract.NodeMetricsQueryPath, path)
	}
	if scheme != "" {
		q.Set(contract.NodeMetricsQueryScheme, scheme)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set(contract.HeaderNodeToken, agentToken)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", errors.New("fetch metrics failed: " + resp.Status + ": " + strings.TrimSpace(string(respBody)))
	}
	return respBody, resp.Header.Get("Content-Type"), nil
}

// Probe asks the node at agentBaseURL to health-probe app/instance's own
// endpoint (listening on the given local port) and relay the upstream result.
// Used by the control-plane health monitor for agent-mode machines, whose app
// ports it cannot reach directly. A transport error to the node itself (node
// offline/unreachable) is returned as err — the caller treats that as a failed
// probe, so an offline node correctly drives its instances unhealthy. When the
// node is reachable the returned NodeProbeResponse carries the app's real
// status (or its own connection error) for the caller to judge.
func Probe(ctx context.Context, agentBaseURL, agentToken, app, instance, scheme, method, path string, port, timeoutMS int) (*contract.NodeProbeResponse, error) {
	base := strings.TrimRight(agentBaseURL, "/")
	if base == "" {
		return nil, errors.New("agent_base_url is empty; cannot probe")
	}
	u, err := url.Parse(base + "/v1/probe/" + url.PathEscape(app) + "/" + url.PathEscape(instance))
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set(contract.NodeProbeQueryPort, strconv.Itoa(port))
	if path != "" {
		q.Set(contract.NodeProbeQueryPath, path)
	}
	if scheme != "" {
		q.Set(contract.NodeProbeQueryScheme, scheme)
	}
	if method != "" {
		q.Set(contract.NodeProbeQueryMethod, method)
	}
	if timeoutMS > 0 {
		q.Set(contract.NodeProbeQueryTimeoutMS, strconv.Itoa(timeoutMS))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(contract.HeaderNodeToken, agentToken)
	// Give the node a little headroom over the app-probe timeout it will apply
	// locally, so the node's own bounded probe returns before this call gives up.
	nodeTimeout := time.Duration(timeoutMS)*time.Millisecond + 10*time.Second
	client := &http.Client{Timeout: nodeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("probe failed: " + resp.Status + ": " + strings.TrimSpace(string(body)))
	}
	var out contract.NodeProbeResponse
	if err := sonic.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
