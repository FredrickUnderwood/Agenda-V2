package node

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// fetchMetricsTimeout bounds a single local scrape. Short relative to the
// log tail's read: a /metrics handler should respond fast, and a hung app
// shouldn't block the node's response to the control plane for long.
const fetchMetricsTimeout = 5 * time.Second

// maxMetricsReadBytes caps how much of a /metrics response body we relay,
// mirroring maxTailReadBytes's reasoning for log files — a runaway exporter
// shouldn't be able to force an unbounded read into memory.
const maxMetricsReadBytes = 2 << 20 // 2MB

var metricsHTTPClient = &http.Client{Timeout: fetchMetricsTimeout}

// fetchLocalMetrics GETs <scheme>://<backendHost>:<port><path> — the app
// instance's own metrics endpoint (sdk/go/metric), reachable only from this
// machine — and returns the raw response body verbatim so it can be relayed
// to Prometheus without agenda-node needing to understand exposition format.
// backendHost mirrors ProxyHandler's: under DooD the app's published port
// lives on the Docker host, not inside node's own container, so it defaults
// to 127.0.0.1 but is configurable (host.docker.internal under DooD). scheme
// defaults to http (sdk/go/metric binds plain HTTP) but is honored so a custom
// TLS metrics endpoint can still be scraped.
func fetchLocalMetrics(ctx context.Context, backendHost, scheme string, port int, path string) (body []byte, contentType string, err error) {
	ctx, cancel := context.WithTimeout(ctx, fetchMetricsTimeout)
	defer cancel()

	if backendHost == "" {
		backendHost = "127.0.0.1"
	}
	if scheme == "" {
		scheme = "http"
	}
	url := fmt.Sprintf("%s://%s:%d%s", scheme, backendHost, port, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := metricsHTTPClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(io.LimitReader(resp.Body, maxMetricsReadBytes))
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("metrics endpoint returned %d", resp.StatusCode)
	}
	return body, resp.Header.Get("Content-Type"), nil
}
