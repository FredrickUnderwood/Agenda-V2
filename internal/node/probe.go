package node

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// defaultProbeTimeout bounds a single local health probe when the caller
// does not specify one. Kept modest — a health endpoint should respond fast,
// and a hung app must not block the node's response to the control plane.
const defaultProbeTimeout = 5 * time.Second

// maxProbeTimeout caps a caller-supplied timeout so a bad value can't pin a
// node goroutine open indefinitely.
const maxProbeTimeout = 30 * time.Second

// probeLocal performs one health probe against <scheme>://<backendHost>:<port><path>
// — the app instance's own health endpoint, reachable only from this machine
// — and reports the upstream status code, latency, and error verbatim. Unlike
// fetchLocalMetrics it does NOT fail on a non-2xx status: the expected-status
// comparison belongs to the control plane, so every reachable response is a
// successful probe that simply carries the app's real status code. A transport
// error (connection refused, timeout) is returned as err with status 0.
//
// backendHost mirrors ProxyHandler's / fetchLocalMetrics's: under DooD the
// app's published port lives on the Docker host, not inside node's own
// container, so it defaults to 127.0.0.1 but is configurable
// (host.docker.internal under DooD).
func probeLocal(ctx context.Context, backendHost, scheme, method, path string, port int, timeout time.Duration) (status, latencyMS int, err error) {
	if backendHost == "" {
		backendHost = "127.0.0.1"
	}
	if scheme == "" {
		scheme = "http"
	}
	if method == "" {
		method = http.MethodGet
	}
	if path == "" {
		path = "/"
	}
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	if timeout > maxProbeTimeout {
		timeout = maxProbeTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	url := fmt.Sprintf("%s://%s:%d%s", scheme, backendHost, port, path)
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return 0, 0, err
	}
	// Each probe uses a fresh client so the caller-supplied timeout is honored
	// per request (the shared context deadline already bounds it, but a fresh
	// client keeps this self-contained and side-effect free).
	client := &http.Client{Timeout: timeout}
	started := time.Now()
	resp, err := client.Do(req)
	latencyMS = int(time.Since(started).Milliseconds())
	if err != nil {
		return 0, latencyMS, err
	}
	// Drain and close so the connection can be reused / released promptly.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	return resp.StatusCode, latencyMS, nil
}
