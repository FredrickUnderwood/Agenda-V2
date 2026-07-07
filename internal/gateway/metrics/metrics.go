// Package metrics defines the gateway's Prometheus instrumentation: a
// request counter and a latency histogram, both labeled by route/service/env
// so operators can compute per-app error rate and P99 latency (see the
// provisioned Grafana "Gateway Overview" dashboard for example PromQL).
package metrics

import (
	"bufio"
	"net"
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	RequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total requests proxied by the gateway.",
		},
		[]string{"route_key", "service_name", "env", "backend", "method", "status_class"},
	)

	RequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_request_duration_seconds",
			Help:    "Latency of requests proxied by the gateway.",
			Buckets: prometheus.DefBuckets, // 5ms .. 10s
		},
		[]string{"route_key", "service_name", "env", "backend", "method"},
	)
)

func init() {
	prometheus.MustRegister(RequestsTotal, RequestDuration)
}

// StatusClass buckets an HTTP status code into "2xx".."5xx" (or "xxx" for an
// out-of-range/unset code) so the status_class label has fixed cardinality.
func StatusClass(code int) string {
	if code < 100 || code > 599 {
		return "xxx"
	}
	return strconv.Itoa(code/100) + "xx"
}

// StatusRecorder wraps an http.ResponseWriter to capture the status code
// ultimately written — by a normal response or by httputil.ReverseProxy's
// ErrorHandler (which receives this same wrapped writer), so callers can
// record metrics once, uniformly, after the proxy call returns. It forwards
// Flush/Hijack so it doesn't break streamed (SSE/chunked) responses or
// WebSocket upgrades proxied through the gateway — ReverseProxy type-asserts
// both on the ResponseWriter it's given.
type StatusRecorder struct {
	http.ResponseWriter
	status int
}

// WrapResponseWriter defaults Status() to 200, matching net/http's own
// behavior when a handler writes a body without calling WriteHeader first.
func WrapResponseWriter(w http.ResponseWriter) *StatusRecorder {
	return &StatusRecorder{ResponseWriter: w, status: http.StatusOK}
}

func (r *StatusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *StatusRecorder) Status() int { return r.status }

func (r *StatusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *StatusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}
