package metric

import (
	"bufio"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// HTTP server instrumentation. Apps get standard per-route QPS / error-rate /
// latency metrics by wrapping their handler (net/http) or adding one middleware
// (framework sub-packages like ginmetric), without hand-rolling counters. The
// `route` label is the matched *route template* (e.g. "/orders/{id}"), not the
// raw path, so cardinality stays bounded by the app's route table rather than
// its traffic — the gateway can only heuristically guess this from the outside,
// whereas the app knows it exactly.
const (
	// unmatchedRoute labels requests that matched no registered route (e.g.
	// gin's c.FullPath() == "" for a 404), keeping them out of the raw-path
	// cardinality explosion they would otherwise cause.
	unmatchedRoute = "unmatched"
	// overflowRoute is the sink once maxDistinctRoutes distinct routes have
	// been seen — a backstop in case an extractor yields raw paths after all.
	overflowRoute = "__overflow__"
)

// maxDistinctRoutes bounds distinct `route` label values per process. A real
// route table is far smaller; this only trips on misuse (an extractor returning
// raw paths), and then fails safe instead of exploding series. A var, not a
// const, so tests can lower it.
var maxDistinctRoutes = 500

var (
	httpRequestsTotal = NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests handled, by route template, method and status code.",
	}, []string{"route", "method", "status"})

	httpRequestDuration = NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency in seconds, by route template and method.",
		Buckets: prometheus.DefBuckets, // 5ms .. 10s
	}, []string{"route", "method"})
)

var (
	routeMu   sync.Mutex
	routeSeen = map[string]struct{}{}
)

// admitRoute enforces the distinct-route cap, mapping empty to "unmatched" and
// any route beyond the cap to "__overflow__".
func admitRoute(route string) string {
	if route == "" {
		return unmatchedRoute
	}
	routeMu.Lock()
	defer routeMu.Unlock()
	if _, ok := routeSeen[route]; ok {
		return route
	}
	if len(routeSeen) >= maxDistinctRoutes {
		routeSeen[overflowRoute] = struct{}{}
		return overflowRoute
	}
	routeSeen[route] = struct{}{}
	return route
}

// ObserveHTTP records one handled HTTP request. Framework middlewares call this
// with the route template they extracted; it is also the escape hatch for any
// framework without a dedicated sub-package (compute route/status yourself and
// call this). status is the numeric HTTP status code.
func ObserveHTTP(route, method string, status int, elapsed time.Duration) {
	route = admitRoute(route)
	httpRequestsTotal.WithLabelValues(route, method, strconv.Itoa(status)).Inc()
	httpRequestDuration.WithLabelValues(route, method).Observe(elapsed.Seconds())
}

// RouteExtractor derives the route-template label from a request, after the
// handler chain has run (so a router had a chance to record the matched route).
type RouteExtractor func(*http.Request) string

// DefaultRouteExtractor reads the Go 1.22+ ServeMux matched pattern
// (r.Pattern), stripping any leading "METHOD " so the label is just the path
// template. Returns "" (→ "unmatched") for requests a ServeMux didn't route.
func DefaultRouteExtractor(r *http.Request) string {
	p := r.Pattern
	if i := strings.IndexByte(p, ' '); i >= 0 {
		p = p[i+1:]
	}
	return p
}

// Middleware wraps next with HTTP server instrumentation for the standard
// library. extract may be nil to use DefaultRouteExtractor (Go 1.22+ ServeMux).
// Because http.ServeMux sets r.Pattern on the same *http.Request it routes,
// this wrapper reads the matched template after next returns even when it sits
// outside the mux.
func Middleware(next http.Handler, extract RouteExtractor) http.Handler {
	if extract == nil {
		extract = DefaultRouteExtractor
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		ObserveHTTP(extract(r), r.Method, rec.status, time.Since(start))
	})
}

// statusRecorder captures the response status. It forwards Flush/Hijack so it
// doesn't break streamed (SSE/chunked) responses or WebSocket upgrades.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status = status
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true // an implicit 200 if WriteHeader was never called
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}
