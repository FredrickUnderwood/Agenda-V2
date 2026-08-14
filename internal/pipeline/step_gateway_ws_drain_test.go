package pipeline

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/gatewayclient"
)

// wsStatsServer stands in for the gateway's GET /-/ws/connections, reporting a
// count that drops to zero after the given number of polls.
func wsStatsServer(t *testing.T, zeroAfter int32) (*gatewayclient.Client, *int32) {
	t.Helper()
	var polls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&polls, 1)
		active := 2
		if n > zeroAfter {
			active = 0
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"active":` + strconv.Itoa(active) + `}`))
	}))
	t.Cleanup(srv.Close)

	cfg := config.GatewayConfig{BaseURL: srv.URL, ServiceToken: "tok"}
	cfg.Timeout.Duration = 2 * time.Second
	return gatewayclient.NewClient(cfg), &polls
}

func TestGatewayWSDrainStep_WaitsUntilTunnelsClose(t *testing.T) {
	client, polls := wsStatsServer(t, 2)
	step := &GatewayWSDrainStep{
		Client:       client,
		InstanceName: "default",
		RouteKeys:    []string{"myapp-prod"},
		Timeout:      5 * time.Second,
		PollInterval: 10 * time.Millisecond,
	}
	rc := &RunContext{Output: &bytes.Buffer{}}

	start := time.Now()
	if err := step.Execute(context.Background(), rc); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("step took %v; it should return as soon as the count hits zero", elapsed)
	}
	if got := atomic.LoadInt32(polls); got < 3 {
		t.Errorf("polls = %d, want at least 3 (two non-zero readings then zero)", got)
	}
}

// The wait is bounded: a client that never goes away must not block a
// decommission forever.
func TestGatewayWSDrainStep_GivesUpAtTimeout(t *testing.T) {
	client, _ := wsStatsServer(t, 1<<20) // never reaches zero
	out := &bytes.Buffer{}
	step := &GatewayWSDrainStep{
		Client:       client,
		InstanceName: "default",
		RouteKeys:    []string{"myapp-prod"},
		Timeout:      150 * time.Millisecond,
		PollInterval: 10 * time.Millisecond,
	}

	start := time.Now()
	if err := step.Execute(context.Background(), &RunContext{Output: out}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Fatalf("step returned after %v; it did not wait out its budget", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("step took %v; it ignored its budget", elapsed)
	}
	if !bytes.Contains(out.Bytes(), []byte("proceeding with teardown")) {
		t.Errorf("timeout was not reported to the deploy log:\n%s", out.String())
	}
}

// An unreachable gateway must not wedge a teardown: the route is already
// drained and the containers are going away regardless.
func TestGatewayWSDrainStep_ContinuesWhenGatewayUnreachable(t *testing.T) {
	cfg := config.GatewayConfig{BaseURL: "http://127.0.0.1:1", ServiceToken: "tok"}
	cfg.Timeout.Duration = 200 * time.Millisecond
	out := &bytes.Buffer{}
	step := &GatewayWSDrainStep{
		Client:       gatewayclient.NewClient(cfg),
		InstanceName: "default",
		Timeout:      5 * time.Second,
		PollInterval: 10 * time.Millisecond,
	}
	if err := step.Execute(context.Background(), &RunContext{Output: out}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("cannot query gateway")) {
		t.Errorf("unreachable gateway not reported:\n%s", out.String())
	}
}

func TestGatewayWSDrainStep_DisabledTimeoutIsNoOp(t *testing.T) {
	client, polls := wsStatsServer(t, 1<<20)
	step := &GatewayWSDrainStep{Client: client, InstanceName: "default", Timeout: 0}
	if err := step.Execute(context.Background(), &RunContext{Output: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := atomic.LoadInt32(polls); got != 0 {
		t.Errorf("polls = %d, want 0 when the wait is disabled", got)
	}
}
