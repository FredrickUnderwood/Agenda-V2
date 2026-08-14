package wstunnel

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestUpgradeProtocol(t *testing.T) {
	cases := []struct {
		name       string
		connection string
		upgrade    string
		proto      int
		want       string
	}{
		{name: "websocket", connection: "Upgrade", upgrade: "websocket", proto: 1, want: "websocket"},
		{name: "case insensitive", connection: "upgrade", upgrade: "WebSocket", proto: 1, want: "websocket"},
		{name: "token in a list", connection: "keep-alive, Upgrade", upgrade: "websocket", proto: 1, want: "websocket"},
		{name: "h2c", connection: "Upgrade", upgrade: "h2c", proto: 1, want: "h2c"},
		{name: "no connection header", upgrade: "websocket", proto: 1, want: ""},
		{name: "no upgrade at all", proto: 1, want: ""},
		// "upgrade-insecure-requests" must not be mistaken for the token.
		{name: "substring is not a token", connection: "upgrade-insecure-requests", upgrade: "websocket", proto: 1, want: ""},
		// HTTP/2 has no Upgrade mechanism; RFC 8441 is out of scope.
		{name: "http/2", connection: "Upgrade", upgrade: "websocket", proto: 2, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.ProtoMajor = tc.proto
			if tc.connection != "" {
				r.Header.Set("Connection", tc.connection)
			}
			if tc.upgrade != "" {
				r.Header.Set("Upgrade", tc.upgrade)
			}
			if got := UpgradeProtocol(r); got != tc.want {
				t.Errorf("UpgradeProtocol = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOriginAllowed(t *testing.T) {
	cases := []struct {
		origin  string
		allowed []string
		want    bool
	}{
		// No allowlist configured: anything goes, including no Origin at all
		// (non-browser clients never send one).
		{"https://anything.example.com", nil, true},
		{"", nil, true},

		{"https://app.example.com", []string{"https://app.example.com"}, true},
		{"HTTPS://APP.EXAMPLE.COM", []string{"https://app.example.com"}, true},
		{"https://app.example.com/", []string{"https://app.example.com"}, true},
		{"http://app.example.com", []string{"https://app.example.com"}, false},
		// Bare host entry matches any scheme.
		{"http://app.example.com", []string{"app.example.com"}, true},
		// Wildcard covers subdomains but not the apex.
		{"https://a.example.com", []string{"*.example.com"}, true},
		{"https://a.b.example.com", []string{"*.example.com"}, true},
		{"https://example.com", []string{"*.example.com"}, false},
		{"https://notexample.com", []string{"*.example.com"}, false},
		{"https://evil.com", []string{"https://app.example.com"}, false},
		{"https://anything", []string{"*"}, true},
		// With an allowlist in force, a missing or opaque Origin is refused —
		// otherwise any non-browser client would bypass the check outright.
		{"", []string{"https://app.example.com"}, false},
		{"null", []string{"https://app.example.com"}, false},
	}
	for _, tc := range cases {
		if got := OriginAllowed(tc.origin, tc.allowed); got != tc.want {
			t.Errorf("OriginAllowed(%q, %v) = %v, want %v", tc.origin, tc.allowed, got, tc.want)
		}
	}
}

func TestRegistryLimits(t *testing.T) {
	reg := NewRegistry()
	key := Key{RouteKey: "api", Instance: "a"}

	first, err := reg.Admit(key, "10.0.0.1", Limits{PerRoute: 2})
	if err != nil {
		t.Fatalf("first admit: %v", err)
	}
	if _, err := reg.Admit(key, "10.0.0.2", Limits{PerRoute: 2}); err != nil {
		t.Fatalf("second admit: %v", err)
	}
	_, err = reg.Admit(key, "10.0.0.3", Limits{PerRoute: 2})
	if Reason(err) != RejectRouteLimit {
		t.Fatalf("third admit reason = %q, want %q", Reason(err), RejectRouteLimit)
	}

	// Releasing frees the slot for the next caller.
	first.Release()
	if _, err := reg.Admit(key, "10.0.0.3", Limits{PerRoute: 2}); err != nil {
		t.Fatalf("admit after release: %v", err)
	}

	// Release is idempotent — it sits in a defer next to explicit cleanup.
	first.Release()
	if got := reg.Active(); got != 2 {
		t.Fatalf("active = %d, want 2 after a double release", got)
	}
}

func TestRegistryPerIPAndTotalLimits(t *testing.T) {
	reg := NewRegistry()
	limits := Limits{Total: 3, PerIP: 1}

	if _, err := reg.Admit(Key{RouteKey: "a"}, "10.0.0.1", limits); err != nil {
		t.Fatalf("admit: %v", err)
	}
	// Same IP, different route: still one client.
	_, err := reg.Admit(Key{RouteKey: "b"}, "10.0.0.1", limits)
	if Reason(err) != RejectClientLimit {
		t.Fatalf("reason = %q, want %q", Reason(err), RejectClientLimit)
	}
	if _, err := reg.Admit(Key{RouteKey: "b"}, "10.0.0.2", limits); err != nil {
		t.Fatalf("different ip: %v", err)
	}
	if _, err := reg.Admit(Key{RouteKey: "c"}, "10.0.0.3", limits); err != nil {
		t.Fatalf("third: %v", err)
	}
	_, err = reg.Admit(Key{RouteKey: "d"}, "10.0.0.4", limits)
	if Reason(err) != RejectTotalLimit {
		t.Fatalf("reason = %q, want %q", Reason(err), RejectTotalLimit)
	}
}

func TestRegistryDrainRejectsAndCloses(t *testing.T) {
	reg := NewRegistry()
	slot, err := reg.Admit(Key{RouteKey: "api", Instance: "a"}, "10.0.0.1", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer client.Close()
	slot.Attach(server)

	reg.BeginDrain()
	if _, err := reg.Admit(Key{RouteKey: "api"}, "10.0.0.2", Limits{}); Reason(err) != RejectDraining {
		t.Fatalf("reason = %q, want %q", Reason(err), RejectDraining)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if forced := reg.Drain(ctx); forced != 1 {
		t.Fatalf("forced = %d, want 1", forced)
	}
	if !slot.Forced() {
		t.Error("slot not marked as force-closed")
	}
	// The attached connection really is closed.
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Error("connection still readable after CloseAll")
	}
}

// Drain returns as soon as the last tunnel goes away, rather than always
// burning its whole budget.
func TestRegistryDrainWaitsForRelease(t *testing.T) {
	reg := NewRegistry()
	slot, err := reg.Admit(Key{RouteKey: "api"}, "", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	reg.BeginDrain()

	go func() {
		time.Sleep(150 * time.Millisecond)
		slot.Release()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if forced := reg.Drain(ctx); forced != 0 {
		t.Fatalf("forced = %d, want 0 — the tunnel ended on its own", forced)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("drain took %v; it should return as soon as the last tunnel ends", elapsed)
	}
}

func TestRegistryStats(t *testing.T) {
	reg := NewRegistry()
	for _, key := range []Key{
		{RouteKey: "api", Instance: "a"},
		{RouteKey: "api", Instance: "a"},
		{RouteKey: "api", Instance: "b"},
		{RouteKey: "web", Instance: "a"},
	} {
		if _, err := reg.Admit(key, "", Limits{}); err != nil {
			t.Fatal(err)
		}
	}
	stats := reg.Stats()
	want := []Stat{
		{RouteKey: "api", Instance: "a", Active: 2},
		{RouteKey: "api", Instance: "b", Active: 1},
		{RouteKey: "web", Instance: "a", Active: 1},
	}
	if len(stats) != len(want) {
		t.Fatalf("stats = %+v, want %+v", stats, want)
	}
	for i := range want {
		if stats[i] != want[i] {
			t.Errorf("stats[%d] = %+v, want %+v", i, stats[i], want[i])
		}
	}
}

func TestRegistryConcurrentAdmitRelease(t *testing.T) {
	reg := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slot, err := reg.Admit(Key{RouteKey: "api", Instance: "a"}, "10.0.0.1", Limits{})
			if err != nil {
				return
			}
			slot.Release()
		}()
	}
	wg.Wait()
	if got := reg.Active(); got != 0 {
		t.Fatalf("active = %d, want 0", got)
	}
	if got := reg.Stats(); len(got) != 0 {
		t.Fatalf("stats = %+v, want empty (counters must not leak keys)", got)
	}
}

func TestIdleConnRefreshesOnTraffic(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	conn := NewIdleConn(a, 200*time.Millisecond)
	idle, ok := conn.(*IdleConn)
	if !ok {
		t.Fatalf("NewIdleConn returned %T, want *IdleConn", conn)
	}

	// Traffic every 100ms keeps a 200ms idle window open past 400ms. net.Pipe
	// is synchronous, so the writes must run on their own goroutine.
	go func() {
		for i := 0; i < 4; i++ {
			time.Sleep(100 * time.Millisecond)
			if _, err := b.Write([]byte("x")); err != nil {
				return
			}
		}
	}()
	buf := make([]byte, 1)
	for i := 0; i < 4; i++ {
		if _, err := conn.Read(buf); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
	if idle.TimedOut() {
		t.Fatal("idle timeout fired despite continuous traffic")
	}

	// Then silence expires it.
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("read succeeded after the idle window")
	}
	if !idle.TimedOut() {
		t.Error("expiry was not attributed to the idle timeout")
	}
}

func TestNewIdleConnPassthroughWhenDisabled(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if got := NewIdleConn(a, 0); got != a {
		t.Fatalf("NewIdleConn(_, 0) = %T, want the connection unwrapped", got)
	}
}

func TestRateLimiter(t *testing.T) {
	if (*RateLimiter)(nil).Allow() != true {
		t.Error("a nil limiter must allow everything")
	}
	if NewRateLimiter(0, 5) != nil {
		t.Error("a non-positive rate must disable limiting")
	}

	l := NewRateLimiter(1000, 2)
	if !l.Allow() || !l.Allow() {
		t.Fatal("burst of 2 was not honoured")
	}
	if l.Allow() {
		t.Fatal("third call within the burst window was allowed")
	}
	// 1000/s refills a token in ~1ms.
	time.Sleep(20 * time.Millisecond)
	if !l.Allow() {
		t.Fatal("bucket did not refill")
	}
}

func TestClientIPIgnoresForwardedFor(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.9:54321"
	r.Header.Set("X-Forwarded-For", "10.0.0.1")
	// Caller-controlled headers must not decide a rate-limit key, or the limit
	// is opt-out for anyone who reads the docs.
	if got := ClientIP(r); got != "203.0.113.9" {
		t.Errorf("ClientIP = %q, want the peer address", got)
	}
}
