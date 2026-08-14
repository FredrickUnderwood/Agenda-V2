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
	// endpoint is the normalized app-relative request path (see endpoint.go),
	// giving per-API-endpoint QPS/error-rate/latency. Service-level views are
	// recovered by aggregating the label away in PromQL (sum by (service_name)).
	RequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total requests proxied by the gateway.",
		},
		[]string{"route_key", "service_name", "env", "backend", "method", "status_class", "endpoint"},
	)

	// backend (instance) is deliberately NOT a label here: the histogram is
	// already multiplied by endpoint × buckets, and per-instance latency
	// percentiles are rarely needed. Per-instance traffic/errors remain visible
	// via RequestsTotal, which keeps backend.
	RequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_request_duration_seconds",
			Help:    "Latency of requests proxied by the gateway.",
			Buckets: prometheus.DefBuckets, // 5ms .. 10s
		},
		[]string{"route_key", "service_name", "env", "method", "endpoint"},
	)

	// WebSocket tunnels get their own instruments rather than riding on the
	// HTTP ones. Two reasons: a tunnel's duration is minutes-to-hours, so it
	// would land in the +Inf bucket of RequestDuration (whose buckets top out
	// at 10s) and destroy that route's latency percentiles; and an HTTP metric
	// is only observable once the request ends, which for a tunnel means the
	// dashboard learns about a connection hours after it opened.
	//
	// WebsocketHandshakes is therefore incremented at handshake time, and
	// WebsocketConnections is a gauge you can read right now.

	// result is one of: success, not_enabled, unsupported_protocol,
	// origin_rejected, backend_refused, draining, total_limit, route_limit,
	// client_limit.
	WebsocketHandshakes = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_websocket_handshakes_total",
			Help: "WebSocket handshakes seen by the gateway, by outcome.",
		},
		[]string{"route_key", "service_name", "env", "backend", "result"},
	)

	WebsocketConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_websocket_connections_active",
			Help: "WebSocket tunnels currently established through the gateway.",
		},
		[]string{"route_key", "service_name", "env", "backend"},
	)

	// Buckets span seconds to ~9 hours: a connection that dies in under a
	// minute and one that lives all day are different failure stories, and the
	// default HTTP buckets can tell neither.
	WebsocketConnectionDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_websocket_connection_duration_seconds",
			Help:    "Lifetime of established WebSocket tunnels.",
			Buckets: []float64{1, 5, 15, 60, 300, 900, 1800, 3600, 7200, 21600, 32400},
		},
		[]string{"route_key", "service_name", "env"},
	)

	// reason is one of: peer_closed, idle_timeout, drain.
	WebsocketDisconnects = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_websocket_disconnects_total",
			Help: "Established WebSocket tunnels that ended, by reason.",
		},
		[]string{"route_key", "service_name", "env", "reason"},
	)
)

func init() {
	prometheus.MustRegister(
		RequestsTotal,
		RequestDuration,
		WebsocketHandshakes,
		WebsocketConnections,
		WebsocketConnectionDuration,
		WebsocketDisconnects,
	)
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
