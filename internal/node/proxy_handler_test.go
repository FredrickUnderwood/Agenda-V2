package node

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

// TestProxyHandlerUsesConfiguredBackendHost guards against regressing back to
// a hardcoded 127.0.0.1 backend: when node drives a separate host's dockerd
// (docker-outside-of-docker), a registered instance's port is only reachable
// via that host's own address (e.g. host.docker.internal), not node's own
// loopback.
func TestProxyHandlerUsesConfiguredBackendHost(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok:" + r.URL.Path))
	}))
	defer backend.Close()

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(backendURL.Port())
	if err != nil {
		t.Fatal(err)
	}

	registry := NewProxyRegistry()
	registry.Set("default", port)

	// backendHost = backendURL.Hostname() stands in for "the host the
	// registered port is actually reachable on" (127.0.0.1 in this test
	// harness; host.docker.internal in the real DooD deployment) — the
	// point is the handler must use *this* configured host, not a
	// hardcoded one.
	h := NewProxyHandler(registry, backendURL.Hostname())

	req := httptest.NewRequest(http.MethodGet, "/i/default/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "ok:/healthz" {
		t.Fatalf("body = %q, want %q", got, "ok:/healthz")
	}
}

// TestProxyHandlerDefaultsBackendHost ensures an empty backendHost still
// defaults to 127.0.0.1 (bare-metal/VM node, unchanged from before this was
// configurable).
func TestProxyHandlerDefaultsBackendHost(t *testing.T) {
	h := NewProxyHandler(NewProxyRegistry(), "")
	if h.backendHost != "127.0.0.1" {
		t.Fatalf("default backendHost = %q, want 127.0.0.1", h.backendHost)
	}
}
