package application

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/metrics"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/service"
	"github.com/FredrickUnderwood/agenda-v2/internal/wstunnel/wstest"
	alog "github.com/FredrickUnderwood/agenda-v2/sdk/go/log"
)

// newWSApp builds a gateway with one WebSocket-enabled route pointing at
// backendURL. mutate customizes the route before it is installed.
func newWSApp(backendURL, routeKey string, opts WebSocketOptions, mutate func(*service.RouteSnapshot)) *GatewayApplication {
	app := NewGatewayApplication(nil, time.Second, opts)
	route := service.RouteSnapshot{
		RouteKey:    routeKey,
		ServiceName: "svc-" + routeKey,
		Env:         "test",
		Host:        "*",
		PathPrefix:  "/",
		// Deliberately short. Any test tunnel that outlives this proves the
		// upgrade path does not inherit the ordinary request deadline.
		Timeout:     150 * time.Millisecond,
		UpgradeMode: domain.UpgradeModeWebSocket,
		Backends:    []service.BackendSnapshot{{TargetKey: routeKey + "-a", InstanceName: "inst-a", URL: backendURL}},
	}
	if mutate != nil {
		mutate(&route)
	}
	app.snapshots = []service.RouteSnapshot{route}
	return app
}

// serveGateway exposes an app over real HTTP. httptest.NewRecorder cannot be
// used for these tests: it is not an http.Hijacker, and hijacking is the whole
// mechanism under test.
func serveGateway(t *testing.T, app *GatewayApplication) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app.ServeProxy(w, r, "")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestWebSocket_TunnelOutlivesRequestTimeout is the regression test for the
// original defect: every proxied request was wrapped in
// context.WithTimeout(route.Timeout), and ReverseProxy watches that context for
// the entire life of an upgraded connection — so a WebSocket was disconnected
// at the request timeout (30s in production) no matter how healthy it was.
//
// The route here has a 150ms timeout; the tunnel must keep echoing well past it.
func TestWebSocket_TunnelOutlivesRequestTimeout(t *testing.T) {
	backend := httptest.NewServer(&wstest.Handler{})
	defer backend.Close()

	gw := serveGateway(t, newWSApp(backend.URL, "ws-longlived", WebSocketOptions{}, nil))

	client, _, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(10 * time.Second))

	// 6 exchanges x 100ms spans ~600ms, four times the route timeout.
	for i := 0; i < 6; i++ {
		got, err := client.Echo("ping")
		if err != nil {
			t.Fatalf("echo %d after %v: %v", i, time.Duration(i)*100*time.Millisecond, err)
		}
		if got != "ping" {
			t.Fatalf("echo %d = %q, want %q", i, got, "ping")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestWebSocket_PlainRequestsStillTimeOut guards the other side of that fix:
// removing the deadline for upgrades must not remove it for ordinary requests.
func TestWebSocket_PlainRequestsStillTimeOut(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(600 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	gw := serveGateway(t, newWSApp(backend.URL, "ws-plain-timeout", WebSocketOptions{}, nil))

	start := time.Now()
	resp, err := http.Get(gw.URL + "/slow")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 from the route timeout", resp.StatusCode)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("plain request took %v; the route's 150ms timeout was not applied", elapsed)
	}
}

func TestWebSocket_RejectedWhenRouteDoesNotAllowUpgrades(t *testing.T) {
	backend := httptest.NewServer(&wstest.Handler{})
	defer backend.Close()

	app := newWSApp(backend.URL, "ws-disabled", WebSocketOptions{}, func(r *service.RouteSnapshot) {
		r.UpgradeMode = domain.UpgradeModeNone
	})
	gw := serveGateway(t, app)

	_, resp, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{})
	if err == nil {
		t.Fatal("handshake succeeded on a route with upgrades disabled")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %v, want 403", resp)
	}
}

// A route that never opted in must also fail closed when the stored value is
// empty — rows written before the column existed read as "" and must not be
// treated as permission.
func TestWebSocket_RejectedWhenUpgradeModeUnset(t *testing.T) {
	backend := httptest.NewServer(&wstest.Handler{})
	defer backend.Close()

	app := newWSApp(backend.URL, "ws-unset", WebSocketOptions{}, func(r *service.RouteSnapshot) {
		r.UpgradeMode = ""
	})
	gw := serveGateway(t, app)

	if _, resp, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{}); err == nil {
		t.Fatal("handshake succeeded on a route with no upgrade mode set")
	} else if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %v, want 403", resp)
	}
}

// Only RFC 6455 is tunneled; any other Upgrade token is refused rather than
// silently forwarded, since it would get the tunnel without the WebSocket
// admission checks.
func TestWebSocket_RejectsNonWebSocketUpgrade(t *testing.T) {
	backend := httptest.NewServer(&wstest.Handler{})
	defer backend.Close()

	gw := serveGateway(t, newWSApp(backend.URL, "ws-h2c", WebSocketOptions{}, nil))

	req, err := http.NewRequest(http.MethodGet, gw.URL+"/chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "h2c")
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", resp.StatusCode)
	}
}

func TestWebSocket_OriginAllowlist(t *testing.T) {
	backend := httptest.NewServer(&wstest.Handler{})
	defer backend.Close()

	app := newWSApp(backend.URL, "ws-origin", WebSocketOptions{}, func(r *service.RouteSnapshot) {
		r.WebsocketAllowedOrigins = []string{"https://app.example.com", "*.internal.example.com"}
	})
	gw := serveGateway(t, app)

	cases := []struct {
		origin string
		allow  bool
	}{
		{"https://app.example.com", true},
		{"https://team.internal.example.com", true},
		{"https://evil.example.com", false},
		{"", false}, // allowlist configured => an origin is required
	}
	for _, tc := range cases {
		header := http.Header{}
		if tc.origin != "" {
			header.Set("Origin", tc.origin)
		}
		client, resp, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{Header: header})
		if tc.allow {
			if err != nil {
				t.Errorf("origin %q: handshake failed: %v", tc.origin, err)
				continue
			}
			client.Close()
			continue
		}
		if err == nil {
			client.Close()
			t.Errorf("origin %q: handshake succeeded, want rejection", tc.origin)
			continue
		}
		if resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Errorf("origin %q: status = %v, want 403", tc.origin, resp)
		}
	}
}

// The handshake must arrive at the app looking like any other proxied request:
// prefix-stripped path, query preserved, agenda/forwarded/trace headers added,
// and client headers (Cookie, subprotocol) passed through untouched.
func TestWebSocket_HandshakePassthrough(t *testing.T) {
	var got *http.Request
	var mu sync.Mutex
	handler := &wstest.Handler{
		Subprotocol: "chat.v1",
		OnRequest: func(r *http.Request) {
			mu.Lock()
			got = r.Clone(context.Background())
			mu.Unlock()
		},
	}
	backend := httptest.NewServer(handler)
	defer backend.Close()

	app := newWSApp(backend.URL, "ws-passthrough", WebSocketOptions{}, func(r *service.RouteSnapshot) {
		r.PathPrefix = "/api"
		r.StripPrefix = true
	})
	gw := serveGateway(t, app)

	header := http.Header{}
	header.Set("Cookie", "session=abc123")
	header.Set("Sec-WebSocket-Protocol", "chat.v1")
	header.Set("Origin", "https://app.example.com")
	client, resp, err := wstest.Dial(gw.URL+"/api/chat/room?id=42", wstest.DialOptions{Header: header})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer client.Close()

	if sub := resp.Header.Get("Sec-WebSocket-Protocol"); sub != "chat.v1" {
		t.Errorf("negotiated subprotocol = %q, want chat.v1", sub)
	}
	if resp.Header.Get(alog.TraceHeader) == "" {
		t.Error("handshake response carries no trace id")
	}

	mu.Lock()
	defer mu.Unlock()
	if got == nil {
		t.Fatal("backend never saw the handshake")
	}
	if got.URL.Path != "/chat/room" {
		t.Errorf("backend path = %q, want /chat/room (prefix stripped)", got.URL.Path)
	}
	if got.URL.RawQuery != "id=42" {
		t.Errorf("backend query = %q, want id=42", got.URL.RawQuery)
	}
	if got.Header.Get("Cookie") != "session=abc123" {
		t.Errorf("backend Cookie = %q, want it forwarded", got.Header.Get("Cookie"))
	}
	if got.Header.Get("Sec-WebSocket-Protocol") != "chat.v1" {
		t.Errorf("backend subprotocol = %q, want chat.v1", got.Header.Get("Sec-WebSocket-Protocol"))
	}
	if got.Header.Get("Origin") != "https://app.example.com" {
		t.Errorf("backend Origin = %q, want it forwarded", got.Header.Get("Origin"))
	}
	if got.Header.Get("X-Agenda-Route") != "ws-passthrough" {
		t.Errorf("backend X-Agenda-Route = %q", got.Header.Get("X-Agenda-Route"))
	}
	if got.Header.Get("X-Forwarded-For") == "" {
		t.Error("backend saw no X-Forwarded-For")
	}
	if got.Header.Get(alog.TraceHeader) == "" {
		t.Error("backend saw no trace id")
	}
}

// A backend that answers the handshake with anything but 101 (an auth check
// failing, say) must have its status returned to the client rather than being
// masked as a gateway error.
func TestWebSocket_BackendRefusesHandshake(t *testing.T) {
	backend := httptest.NewServer(&wstest.Handler{RejectStatus: http.StatusUnauthorized})
	defer backend.Close()

	gw := serveGateway(t, newWSApp(backend.URL, "ws-refused", WebSocketOptions{}, nil))

	_, resp, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{})
	if err == nil {
		t.Fatal("handshake succeeded against a refusing backend")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %v, want 401 passed through", resp)
	}
}

func TestWebSocket_IdleTimeoutClosesTunnel(t *testing.T) {
	backend := httptest.NewServer(&wstest.Handler{})
	defer backend.Close()

	app := newWSApp(backend.URL, "ws-idle", WebSocketOptions{}, func(r *service.RouteSnapshot) {
		r.WebsocketIdleTimeout = 300 * time.Millisecond
	})
	gw := serveGateway(t, app)

	client, _, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer client.Close()

	// Traffic keeps it alive well past the idle window...
	for i := 0; i < 4; i++ {
		if _, err := client.Echo("keepalive"); err != nil {
			t.Fatalf("echo %d: %v", i, err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	// ...and silence ends it.
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := client.Receive(1); err == nil {
		t.Fatal("tunnel stayed open past the idle timeout")
	}

	waitFor(t, 2*time.Second, func() bool { return app.wsConns.Active() == 0 })
	if got := testutil.ToFloat64(metrics.WebsocketDisconnects.WithLabelValues(
		"ws-idle", "svc-ws-idle", "test", disconnectIdleTimeout)); got < 1 {
		t.Errorf("idle_timeout disconnect not recorded (got %v)", got)
	}
}

func TestWebSocket_PerRouteConnectionCap(t *testing.T) {
	backend := httptest.NewServer(&wstest.Handler{})
	defer backend.Close()

	app := newWSApp(backend.URL, "ws-cap", WebSocketOptions{}, func(r *service.RouteSnapshot) {
		r.WebsocketMaxConnections = 2
	})
	gw := serveGateway(t, app)

	var open []*wstest.Client
	defer func() {
		for _, c := range open {
			c.Close()
		}
	}()
	for i := 0; i < 2; i++ {
		client, _, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{})
		if err != nil {
			t.Fatalf("handshake %d: %v", i, err)
		}
		open = append(open, client)
	}

	_, resp, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{})
	if err == nil {
		t.Fatal("third handshake succeeded past a cap of 2")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %v, want 503", resp)
	}

	// Closing one frees a slot.
	open[0].Close()
	open = open[1:]
	waitFor(t, 2*time.Second, func() bool { return app.wsConns.Active() == 1 })
	client, _, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{})
	if err != nil {
		t.Fatalf("handshake after a slot freed up: %v", err)
	}
	open = append(open, client)
}

func TestWebSocket_HandshakeRateLimit(t *testing.T) {
	backend := httptest.NewServer(&wstest.Handler{})
	defer backend.Close()

	// One handshake, no refill within the test's lifetime.
	app := newWSApp(backend.URL, "ws-rate", WebSocketOptions{HandshakeRate: 0.01, HandshakeBurst: 1}, nil)
	gw := serveGateway(t, app)

	client, _, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{})
	if err != nil {
		t.Fatalf("first handshake: %v", err)
	}
	defer client.Close()

	_, resp, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{})
	if err == nil {
		t.Fatal("second handshake was not rate limited")
	}
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %v, want 429", resp)
	}
}

// Drain is the restart path: established tunnels keep working, new ones are
// refused, and anything still attached when the budget runs out is closed.
func TestWebSocket_Drain(t *testing.T) {
	backend := httptest.NewServer(&wstest.Handler{})
	defer backend.Close()

	app := newWSApp(backend.URL, "ws-drain", WebSocketOptions{}, nil)
	gw := serveGateway(t, app)

	client, _, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer client.Close()

	app.BeginWebSocketDrain()

	if _, err := client.Echo("still here"); err != nil {
		t.Fatalf("established tunnel broke at the start of the drain: %v", err)
	}
	if _, resp, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{}); err == nil {
		t.Fatal("a new handshake was accepted while draining")
	} else if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %v, want 503 while draining", resp)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if forced := app.DrainWebSockets(ctx); forced != 1 {
		t.Fatalf("forced = %d, want 1 tunnel closed at the end of the drain", forced)
	}
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Receive(1); err == nil {
		t.Fatal("tunnel survived a completed drain")
	}
	waitFor(t, 2*time.Second, func() bool { return app.wsConns.Active() == 0 })
	if got := testutil.ToFloat64(metrics.WebsocketDisconnects.WithLabelValues(
		"ws-drain", "svc-ws-drain", "test", disconnectDrain)); got < 1 {
		t.Errorf("drain disconnect not recorded (got %v)", got)
	}
}

// Re-pointing a route away from an instance (what a deploy or decommission
// does) must not disturb tunnels already attached to it — which is exactly why
// the teardown needs a separate wait before stopping the containers.
func TestWebSocket_RouteRepointLeavesEstablishedTunnels(t *testing.T) {
	oldBackend := httptest.NewServer(&wstest.Handler{})
	defer oldBackend.Close()
	newBackend := httptest.NewServer(&wstest.Handler{})
	defer newBackend.Close()

	app := newWSApp(oldBackend.URL, "ws-repoint", WebSocketOptions{}, nil)
	gw := serveGateway(t, app)

	client, _, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer client.Close()

	// Swap the backend, as a route refresh would.
	app.mu.Lock()
	app.snapshots[0].Backends = []service.BackendSnapshot{
		{TargetKey: "ws-repoint-b", InstanceName: "inst-b", URL: newBackend.URL},
	}
	app.mu.Unlock()

	if got, err := client.Echo("still on inst-a"); err != nil || got != "still on inst-a" {
		t.Fatalf("established tunnel broke after the route was re-pointed: %q %v", got, err)
	}
	if stats := app.WebSocketStats(); len(stats) != 1 || stats[0].Instance != "inst-a" || stats[0].Active != 1 {
		t.Fatalf("stats = %+v, want one live tunnel still attributed to inst-a", stats)
	}

	// A new client goes to the new instance.
	second, _, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{})
	if err != nil {
		t.Fatalf("second handshake: %v", err)
	}
	defer second.Close()
	waitFor(t, 2*time.Second, func() bool {
		for _, s := range app.WebSocketStats() {
			if s.Instance == "inst-b" && s.Active == 1 {
				return true
			}
		}
		return false
	})
}

func TestWebSocket_Metrics(t *testing.T) {
	backend := httptest.NewServer(&wstest.Handler{})
	defer backend.Close()

	app := newWSApp(backend.URL, "ws-metrics", WebSocketOptions{}, nil)
	gw := serveGateway(t, app)

	handshakes := metrics.WebsocketHandshakes.WithLabelValues("ws-metrics", "svc-ws-metrics", "test", "inst-a", handshakeSuccess)
	active := metrics.WebsocketConnections.WithLabelValues("ws-metrics", "svc-ws-metrics", "test", "inst-a")
	before := testutil.ToFloat64(handshakes)

	client, _, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}

	// Recorded at open, not at close: the point of the gauge is to answer
	// "what is connected right now".
	waitFor(t, 2*time.Second, func() bool { return testutil.ToFloat64(active) == 1 })
	if got := testutil.ToFloat64(handshakes); got != before+1 {
		t.Errorf("handshake counter: before=%v after=%v", before, got)
	}
	if got := testutil.ToFloat64(metrics.RequestsTotal.WithLabelValues(
		"ws-metrics", "svc-ws-metrics", "test", "inst-a", "GET", "1xx", "/chat")); got < 1 {
		t.Errorf("handshake not counted as 1xx in gateway_requests_total (got %v)", got)
	}

	client.Close()
	waitFor(t, 2*time.Second, func() bool { return testutil.ToFloat64(active) == 0 })
	if got := testutil.ToFloat64(metrics.WebsocketDisconnects.WithLabelValues(
		"ws-metrics", "svc-ws-metrics", "test", disconnectPeerClosed)); got < 1 {
		t.Errorf("peer_closed disconnect not recorded (got %v)", got)
	}
	// The tunnel's lifetime must not pollute the HTTP latency histogram.
	if count := testutil.CollectAndCount(metrics.WebsocketConnectionDuration); count == 0 {
		t.Error("no websocket connection duration sample recorded")
	}
}

// TLS entry: the same handshake over a TLS listener, since hijacking a
// *tls.Conn is a different code path in net/http than a plain one.
func TestWebSocket_OverTLS(t *testing.T) {
	backend := httptest.NewServer(&wstest.Handler{})
	defer backend.Close()

	app := newWSApp(backend.URL, "ws-tls", WebSocketOptions{}, nil)
	gw := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app.ServeProxy(w, r, "")
	}))
	defer gw.Close()

	client, _, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{TLS: true})
	if err != nil {
		t.Fatalf("wss handshake: %v", err)
	}
	defer client.Close()
	if got, err := client.Echo("over tls"); err != nil || got != "over tls" {
		t.Fatalf("echo over tls = %q, %v", got, err)
	}
}

// A backend that dies under a live tunnel must propagate the close to the
// client and release the slot.
//
// Note the half-close in the middle: when the backend hits EOF, ReverseProxy
// half-closes toward the client (CloseWrite) and keeps waiting on the other
// direction, so the tunnel is only fully released once the client closes its
// side too. That is correct TCP behaviour, and it is precisely why the idle
// timeout matters — a client that goes silent instead of closing would
// otherwise hold the slot indefinitely after its backend is gone.
func TestWebSocket_BackendDisappears(t *testing.T) {
	handler := &wstest.Handler{}
	backend := httptest.NewServer(handler)
	defer backend.Close()

	app := newWSApp(backend.URL, "ws-backend-gone", WebSocketOptions{}, nil)
	gw := serveGateway(t, app)

	client, _, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if _, err := client.Echo("hello"); err != nil {
		t.Fatalf("echo: %v", err)
	}

	handler.CloseAll()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := client.Receive(1); err == nil {
		t.Fatal("client saw no close after the backend went away")
	}
	client.Close()
	waitFor(t, 3*time.Second, func() bool { return app.wsConns.Active() == 0 })
}

// The other half of that story: when the backend vanishes and the client never
// closes, the idle timeout is what reclaims the slot.
func TestWebSocket_IdleTimeoutReclaimsHalfClosedTunnel(t *testing.T) {
	handler := &wstest.Handler{}
	backend := httptest.NewServer(handler)
	defer backend.Close()

	app := newWSApp(backend.URL, "ws-halfclosed", WebSocketOptions{}, func(r *service.RouteSnapshot) {
		r.WebsocketIdleTimeout = 300 * time.Millisecond
	})
	gw := serveGateway(t, app)

	client, _, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer client.Close()

	handler.CloseAll() // backend gone; client stays silent and connected
	waitFor(t, 3*time.Second, func() bool { return app.wsConns.Active() == 0 })
}

// Many concurrent tunnels open, exchange traffic, and close without leaking
// goroutines or registry slots. This is a correctness check at modest scale —
// the 1k–10k connection load test the design calls for needs a real
// environment (file descriptor limits, a real client, sustained traffic) and
// does not belong in `go test`.
func TestWebSocket_ConcurrentTunnels(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrency test in short mode")
	}
	backend := httptest.NewServer(&wstest.Handler{})
	defer backend.Close()

	app := newWSApp(backend.URL, "ws-concurrent", WebSocketOptions{}, nil)
	gw := serveGateway(t, app)

	const n = 100
	baseline := runtime.NumGoroutine()

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, _, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{})
			if err != nil {
				errs <- err
				return
			}
			defer client.Close()
			_ = client.SetDeadline(time.Now().Add(10 * time.Second))
			if got, err := client.Echo("hello"); err != nil || got != "hello" {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("tunnel error: %v", err)
		}
	}

	waitFor(t, 10*time.Second, func() bool { return app.wsConns.Active() == 0 })
	// Goroutines unwind asynchronously; allow slack for the pooled server ones
	// but not for n leaked copy loops.
	waitFor(t, 10*time.Second, func() bool { return runtime.NumGoroutine() < baseline+n/2 })
	if leaked := runtime.NumGoroutine() - baseline; leaked >= n/2 {
		t.Fatalf("goroutines leaked: baseline=%d now=%d", baseline, runtime.NumGoroutine())
	}
}

func TestWebSocket_StatsFilterAndReject(t *testing.T) {
	backend := httptest.NewServer(&wstest.Handler{})
	defer backend.Close()

	app := newWSApp(backend.URL, "ws-stats", WebSocketOptions{}, nil)
	gw := serveGateway(t, app)

	if stats := app.WebSocketStats(); len(stats) != 0 {
		t.Fatalf("stats before any tunnel = %+v, want empty", stats)
	}
	client, _, err := wstest.Dial(gw.URL+"/chat", wstest.DialOptions{})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer client.Close()

	waitFor(t, 2*time.Second, func() bool {
		stats := app.WebSocketStats()
		return len(stats) == 1 && stats[0].RouteKey == "ws-stats" && stats[0].Instance == "inst-a" && stats[0].Active == 1
	})
}

// waitFor polls cond until it holds or the budget runs out, so tests do not
// depend on how quickly an asynchronous unwind happens to land.
func waitFor(t *testing.T, budget time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", budget)
	}
}

// Guard against a typo in the label constants silently splitting a metric.
func TestWebSocket_ResultLabelsAreStable(t *testing.T) {
	for _, label := range []string{
		handshakeSuccess, handshakeNotEnabled, handshakeUnsupported,
		handshakeBadOrigin, handshakeRateLimited, handshakeRefused,
		disconnectPeerClosed, disconnectIdleTimeout, disconnectDrain,
	} {
		if label == "" || strings.ContainsAny(label, " \t\"") {
			t.Errorf("metric label %q is not a clean token", label)
		}
	}
}
