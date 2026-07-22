package application

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/metrics"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/service"
)

func newTestApp(t *testing.T, backendURL string, routeKey, instanceName string) *GatewayApplication {
	t.Helper()
	app := NewGatewayApplication(nil, time.Second)
	app.snapshots = []service.RouteSnapshot{
		{
			RouteKey:    routeKey,
			ServiceName: "svc-" + routeKey,
			Env:         "test",
			Host:        "*",
			PathPrefix:  "/",
			Timeout:     5 * time.Second,
			Backends:    []service.BackendSnapshot{{TargetKey: routeKey + "-a", InstanceName: instanceName, URL: backendURL}},
		},
	}
	return app
}

func TestServeProxy_RecordsSuccessMetrics(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	app := newTestApp(t, backend.URL, "metrics-success", "default")

	beforeCount := testutil.ToFloat64(metrics.RequestsTotal.WithLabelValues("metrics-success", "svc-metrics-success", "test", "default", "GET", "2xx", "/orders"))
	beforeSamples := testutil.CollectAndCount(metrics.RequestDuration)

	req := httptest.NewRequest(http.MethodGet, "http://api.example.com/orders", nil)
	rec := httptest.NewRecorder()
	app.ServeProxy(rec, req, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	afterCount := testutil.ToFloat64(metrics.RequestsTotal.WithLabelValues("metrics-success", "svc-metrics-success", "test", "default", "GET", "2xx", "/orders"))
	if afterCount != beforeCount+1 {
		t.Fatalf("gateway_requests_total{2xx} did not increment: before=%v after=%v", beforeCount, afterCount)
	}
	afterSamples := testutil.CollectAndCount(metrics.RequestDuration)
	if afterSamples <= beforeSamples {
		t.Fatalf("gateway_request_duration_seconds did not record a new sample: before=%d after=%d", beforeSamples, afterSamples)
	}
}

func TestServeProxy_RecordsErrorMetrics(t *testing.T) {
	app := newTestApp(t, "http://127.0.0.1:1", "metrics-error", "default")

	before := testutil.ToFloat64(metrics.RequestsTotal.WithLabelValues("metrics-error", "svc-metrics-error", "test", "default", "GET", "5xx", "/orders"))

	req := httptest.NewRequest(http.MethodGet, "http://api.example.com/orders", nil)
	rec := httptest.NewRecorder()
	app.ServeProxy(rec, req, "")

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	after := testutil.ToFloat64(metrics.RequestsTotal.WithLabelValues("metrics-error", "svc-metrics-error", "test", "default", "GET", "5xx", "/orders"))
	if after != before+1 {
		t.Fatalf("gateway_requests_total{5xx} did not increment: before=%v after=%v", before, after)
	}
}

// TestMetricsEndpoint_ExposesRealScrapeFormat drives a request through
// ServeProxy and then scrapes promhttp.Handler() over real HTTP — the same
// handler the gateway registers at /-/metrics (internal/gateway/handler)
// — to confirm what a real Prometheus server would actually see on the wire,
// not just the in-process counter value.
func TestMetricsEndpoint_ExposesRealScrapeFormat(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	app := newTestApp(t, backend.URL, "metrics-scrape", "default")
	req := httptest.NewRequest(http.MethodGet, "http://api.example.com/orders", nil)
	app.ServeProxy(httptest.NewRecorder(), req, "")

	metricsSrv := httptest.NewServer(promhttp.Handler())
	defer metricsSrv.Close()

	resp, err := http.Get(metricsSrv.URL)
	if err != nil {
		t.Fatalf("scrape /-/metrics equivalent: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scrape status = %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scrape body: %v", err)
	}
	text := string(raw)

	if !strings.Contains(text, `# TYPE gateway_requests_total counter`) {
		t.Error("missing gateway_requests_total TYPE line — not valid Prometheus exposition format")
	}
	if !strings.Contains(text, `# TYPE gateway_request_duration_seconds histogram`) {
		t.Error("missing gateway_request_duration_seconds TYPE line — not valid Prometheus exposition format")
	}
	if !strings.Contains(text, `route_key="metrics-scrape"`) {
		t.Errorf("scraped output missing our recorded series (route_key=metrics-scrape):\n%s", text)
	}
	if !strings.Contains(text, `status_class="2xx"`) {
		t.Errorf("scraped output missing status_class label:\n%s", text)
	}
	if !strings.Contains(text, `endpoint="/orders"`) {
		t.Errorf("scraped output missing endpoint label:\n%s", text)
	}
}

// TestServeProxy_EndpointNormalizesIDs confirms distinct ID paths collapse to a
// single endpoint series (so per-endpoint metrics stay bounded), and that a
// strip-prefix route labels by the app-relative path.
func TestServeProxy_EndpointNormalizesIDs(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	app := newTestApp(t, backend.URL, "metrics-endpoint", "default")

	label := []string{"metrics-endpoint", "svc-metrics-endpoint", "test", "default", "GET", "2xx", "/orders/:id"}
	before := testutil.ToFloat64(metrics.RequestsTotal.WithLabelValues(label...))

	for _, id := range []string{"1", "2", "999999"} {
		req := httptest.NewRequest(http.MethodGet, "http://api.example.com/orders/"+id, nil)
		app.ServeProxy(httptest.NewRecorder(), req, "")
	}

	after := testutil.ToFloat64(metrics.RequestsTotal.WithLabelValues(label...))
	if after != before+3 {
		t.Fatalf("expected 3 requests folded into endpoint=/orders/:id, before=%v after=%v", before, after)
	}
}

func TestStatusClass(t *testing.T) {
	cases := map[int]string{
		200: "2xx",
		201: "2xx",
		301: "3xx",
		404: "4xx",
		502: "5xx",
		0:   "xxx",
		999: "xxx",
	}
	for code, want := range cases {
		if got := metrics.StatusClass(code); got != want {
			t.Errorf("StatusClass(%d) = %q, want %q", code, got, want)
		}
	}
}
