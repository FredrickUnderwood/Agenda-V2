package application

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/node"
	"github.com/FredrickUnderwood/agenda-v2/internal/wstunnel/wstest"
)

// TestWebSocket_TwoHopThroughNodeRelay exercises the real production topology:
//
//	client → gateway → agenda-node /i/<instance> relay → app
//
// Both proxies hijack and tunnel, and both had to be fixed for this to work at
// all. Testing them separately would miss the interesting failure: an upgrade
// that survives the first hop and is quietly downgraded (or timed out) by the
// second — which is what the node's shared, HTTP/1.1-pinned WebSocket transport
// exists to prevent.
func TestWebSocket_TwoHopThroughNodeRelay(t *testing.T) {
	var seen *http.Request
	app := httptest.NewServer(&wstest.Handler{
		OnRequest: func(r *http.Request) { seen = r.Clone(r.Context()) },
	})
	defer app.Close()

	appURL, err := url.Parse(app.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(appURL.Port())
	if err != nil {
		t.Fatal(err)
	}
	registry := node.NewProxyRegistry()
	registry.Set("chat-prod-default", port)
	relayHandler := node.NewProxyHandler(registry, appURL.Hostname())
	relay := httptest.NewServer(relayHandler)
	defer relay.Close()

	// The gateway backend is the node's stable /i/<key> path, exactly as
	// pipeline.resolveBackend builds it for an agent-mode machine.
	gwApp := newWSApp(relay.URL+"/i/chat-prod-default", "ws-twohop", WebSocketOptions{}, nil)
	gw := serveGateway(t, gwApp)

	client, resp, err := wstest.Dial(gw.URL+"/chat/room?id=7", wstest.DialOptions{})
	if err != nil {
		t.Fatalf("two-hop handshake: %v", err)
	}
	defer client.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}

	_ = client.SetDeadline(time.Now().Add(10 * time.Second))
	for i := 0; i < 5; i++ {
		if got, err := client.Echo("two-hop"); err != nil || got != "two-hop" {
			t.Fatalf("echo %d = %q, %v", i, got, err)
		}
		time.Sleep(80 * time.Millisecond)
	}

	if seen == nil {
		t.Fatal("app never saw the handshake")
	}
	if seen.URL.Path != "/chat/room" {
		t.Errorf("app path = %q, want /chat/room", seen.URL.Path)
	}
	if seen.URL.RawQuery != "id=7" {
		t.Errorf("app query = %q, want id=7", seen.URL.RawQuery)
	}
	if seen.Header.Get("X-Agenda-Route") != "ws-twohop" {
		t.Errorf("app X-Agenda-Route = %q", seen.Header.Get("X-Agenda-Route"))
	}

	// Both hops account for the same tunnel.
	if got := gwApp.wsConns.Active(); got != 1 {
		t.Errorf("gateway active tunnels = %d, want 1", got)
	}
	if got := relayHandler.ActiveTunnels(); got != 1 {
		t.Errorf("relay active tunnels = %d, want 1", got)
	}

	client.Close()
	waitFor(t, 5*time.Second, func() bool {
		return gwApp.wsConns.Active() == 0 && relayHandler.ActiveTunnels() == 0
	})
}
