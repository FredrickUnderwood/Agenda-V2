package node

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/wstunnel/wstest"
)

// newRelay wires a ProxyHandler in front of an app listening on app's port,
// registered under instance — the same shape the deploy pipeline creates.
func newRelay(t *testing.T, instance string, app *httptest.Server) (*ProxyHandler, *httptest.Server) {
	t.Helper()
	appURL, err := url.Parse(app.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(appURL.Port())
	if err != nil {
		t.Fatal(err)
	}
	registry := NewProxyRegistry()
	registry.Set(instance, port)

	handler := NewProxyHandler(registry, appURL.Hostname())
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return handler, srv
}

func TestProxyHandler_RelaysWebSocket(t *testing.T) {
	var seenPath string
	app := httptest.NewServer(&wstest.Handler{
		OnRequest: func(r *http.Request) { seenPath = r.URL.Path },
	})
	defer app.Close()

	handler, relay := newRelay(t, "svc-default", app)

	client, _, err := wstest.Dial(relay.URL+"/i/svc-default/chat", wstest.DialOptions{})
	if err != nil {
		t.Fatalf("handshake through relay: %v", err)
	}
	defer client.Close()

	if got, err := client.Echo("through the node"); err != nil || got != "through the node" {
		t.Fatalf("echo = %q, %v", got, err)
	}
	if seenPath != "/chat" {
		t.Errorf("app saw path %q, want /chat (instance prefix stripped)", seenPath)
	}
	if handler.ActiveTunnels() != 1 {
		t.Errorf("active tunnels = %d, want 1", handler.ActiveTunnels())
	}

	client.Close()
	waitFor(t, func() bool { return handler.ActiveTunnels() == 0 })
}

// The relay must not second-guess the gateway's policy: it has no route
// config, so it forwards any upgrade the gateway let through.
func TestProxyHandler_RelayDoesNotGateUpgrades(t *testing.T) {
	app := httptest.NewServer(&wstest.Handler{})
	defer app.Close()

	_, relay := newRelay(t, "svc-default", app)

	client, _, err := wstest.Dial(relay.URL+"/i/svc-default/anything", wstest.DialOptions{
		Header: http.Header{"Origin": []string{"https://not-in-any-allowlist.example.com"}},
	})
	if err != nil {
		t.Fatalf("relay refused an upgrade it should have forwarded: %v", err)
	}
	client.Close()
}

func TestProxyHandler_UnknownInstanceRejectsUpgrade(t *testing.T) {
	app := httptest.NewServer(&wstest.Handler{})
	defer app.Close()

	_, relay := newRelay(t, "svc-default", app)

	_, resp, err := wstest.Dial(relay.URL+"/i/does-not-exist/chat", wstest.DialOptions{})
	if err == nil {
		t.Fatal("handshake succeeded for an unregistered instance")
	}
	if resp == nil || resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %v, want 502", resp)
	}
}

func TestProxyHandler_Drain(t *testing.T) {
	app := httptest.NewServer(&wstest.Handler{})
	defer app.Close()

	handler, relay := newRelay(t, "svc-default", app)

	client, _, err := wstest.Dial(relay.URL+"/i/svc-default/chat", wstest.DialOptions{})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer client.Close()

	handler.BeginDrain()

	if got, err := client.Echo("still alive"); err != nil || got != "still alive" {
		t.Fatalf("established tunnel broke when the drain started: %q %v", got, err)
	}
	if _, resp, err := wstest.Dial(relay.URL+"/i/svc-default/chat", wstest.DialOptions{}); err == nil {
		t.Fatal("relay accepted a new tunnel while draining")
	} else if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %v, want 503", resp)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if forced := handler.Drain(ctx); forced != 1 {
		t.Fatalf("forced = %d, want 1", forced)
	}
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Receive(1); err == nil {
		t.Fatal("tunnel survived the relay drain")
	}
}

// Plain HTTP through the relay must be unaffected by the WebSocket handling.
func TestProxyHandler_PlainHTTPStillWorks(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("plain:" + r.URL.Path))
	}))
	defer app.Close()

	_, relay := newRelay(t, "svc-default", app)

	resp, err := http.Get(relay.URL + "/i/svc-default/healthz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
