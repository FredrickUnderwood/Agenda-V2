package metric

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestObserveHTTP_RecordsBothMetrics(t *testing.T) {
	before := testutil.ToFloat64(httpRequestsTotal.WithLabelValues("/orders/{id}", "GET", "200"))
	beforeH := testutil.CollectAndCount(httpRequestDuration)

	ObserveHTTP("/orders/{id}", "GET", 200, 12_000_000) // 12ms

	after := testutil.ToFloat64(httpRequestsTotal.WithLabelValues("/orders/{id}", "GET", "200"))
	if after != before+1 {
		t.Fatalf("http_requests_total not incremented: before=%v after=%v", before, after)
	}
	if afterH := testutil.CollectAndCount(httpRequestDuration); afterH <= beforeH {
		t.Fatalf("http_request_duration_seconds not observed: before=%d after=%d", beforeH, afterH)
	}
}

func TestObserveHTTP_EmptyRouteBecomesUnmatched(t *testing.T) {
	before := testutil.ToFloat64(httpRequestsTotal.WithLabelValues(unmatchedRoute, "GET", "404"))
	ObserveHTTP("", "GET", 404, 1)
	after := testutil.ToFloat64(httpRequestsTotal.WithLabelValues(unmatchedRoute, "GET", "404"))
	if after != before+1 {
		t.Fatalf("empty route not mapped to %q: before=%v after=%v", unmatchedRoute, before, after)
	}
}

func TestAdmitRoute_DistinctCap(t *testing.T) {
	// Fresh guard state so the global map from other tests doesn't skew the cap.
	routeMu.Lock()
	routeSeen = map[string]struct{}{}
	routeMu.Unlock()

	orig := maxDistinctRoutes
	maxDistinctRoutes = 3
	defer func() { maxDistinctRoutes = orig }()

	for _, r := range []string{"/a", "/b", "/c"} {
		if got := admitRoute(r); got != r {
			t.Fatalf("admitRoute(%q)=%q, want passthrough", r, got)
		}
	}
	if got := admitRoute("/d"); got != overflowRoute {
		t.Fatalf("4th distinct route = %q, want %q", got, overflowRoute)
	}
	if got := admitRoute("/a"); got != "/a" {
		t.Fatalf("already-seen route dropped after cap: %q", got)
	}
}

func TestDefaultRouteExtractor_StripsMethod(t *testing.T) {
	cases := map[string]string{
		"/healthz":       "/healthz",
		"GET /orders/{id}": "/orders/{id}",
		"":               "",
	}
	for pattern, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "http://x/", nil)
		r.Pattern = pattern
		if got := DefaultRouteExtractor(r); got != want {
			t.Errorf("DefaultRouteExtractor(pattern=%q)=%q, want %q", pattern, got, want)
		}
	}
}

func TestMiddleware_UsesMatchedPattern(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	h := Middleware(mux, nil)

	label := []string{"/orders/{id}", "GET", strconv.Itoa(http.StatusCreated)}
	before := testutil.ToFloat64(httpRequestsTotal.WithLabelValues(label...))

	// Two different IDs must fold into the one route-template series.
	for _, id := range []string{"1", "2"} {
		req := httptest.NewRequest(http.MethodGet, "http://x/orders/"+id, nil)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	after := testutil.ToFloat64(httpRequestsTotal.WithLabelValues(label...))
	if after != before+2 {
		t.Fatalf("expected 2 requests folded into route=/orders/{id}, before=%v after=%v", before, after)
	}
}
