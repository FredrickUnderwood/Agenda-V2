package application

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/metrics"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/service"
	"github.com/FredrickUnderwood/agenda-v2/internal/wstunnel"
	alog "github.com/FredrickUnderwood/agenda-v2/sdk/go/log"
	"go.uber.org/zap"
)

// Handshake outcomes, used as the `result` label on
// gateway_websocket_handshakes_total. Kept as constants so the dashboard's
// label values and the code that emits them can't drift.
const (
	handshakeSuccess     = "success"
	handshakeNotEnabled  = "not_enabled"
	handshakeUnsupported = "unsupported_protocol"
	handshakeBadOrigin   = "origin_rejected"
	handshakeRateLimited = "rate_limited"
	handshakeRefused     = "backend_refused"
)

// Disconnect reasons, used as the `reason` label on
// gateway_websocket_disconnects_total.
const (
	disconnectPeerClosed  = "peer_closed"
	disconnectIdleTimeout = "idle_timeout"
	disconnectDrain       = "drain"
)

// serveUpgrade handles a request that asked to change protocols.
//
// The two things that make it different from serveRequest, and the reason it is
// a separate path rather than a flag:
//
//  1. No total deadline. serveRequest wraps the request context in
//     context.WithTimeout(route.Timeout); ReverseProxy watches that context for
//     the whole life of an upgraded connection and closes the backend when it
//     fires, so applying it here would not time out a slow tunnel — it would
//     schedule every tunnel's death at route.Timeout (30s by default). The
//     bound a long-lived connection actually needs is an *idle* timeout, which
//     rides on the connection itself (wstunnel.IdleConn).
//
//  2. Admission control. An ordinary request occupies the gateway for
//     milliseconds; a tunnel occupies it, plus a connection on the node relay,
//     for as long as it lives. So it is gated on the route opting in, on the
//     Origin allowlist, on a handshake rate limit, and on connection caps —
//     all before a single byte reaches the backend.
func (a *GatewayApplication) serveUpgrade(
	w http.ResponseWriter,
	r *http.Request,
	route service.RouteSnapshot,
	backend service.BackendSnapshot,
	target *url.URL,
	traceID string,
	proto string,
) {
	// Anything that is not RFC 6455 is refused outright rather than forwarded.
	// The gateway's contract for an upgraded route is "opaque byte tunnel with
	// a WebSocket handshake in front"; h2c or any other protocol would get the
	// tunnel without any of the accounting that makes it safe.
	if proto != wstunnel.ProtocolWebSocket {
		a.rejectHandshake(w, r, route, backend, handshakeUnsupported, http.StatusNotImplemented,
			"only websocket upgrades are supported")
		return
	}
	if !route.AllowsWebSocket() {
		a.rejectHandshake(w, r, route, backend, handshakeNotEnabled, http.StatusForbidden,
			"websocket is not enabled for this route")
		return
	}
	if !wstunnel.OriginAllowed(r.Header.Get("Origin"), route.WebsocketAllowedOrigins) {
		a.rejectHandshake(w, r, route, backend, handshakeBadOrigin, http.StatusForbidden,
			"origin is not allowed for this route")
		return
	}
	if !a.wsLimiter.Allow() {
		a.rejectHandshake(w, r, route, backend, handshakeRateLimited, http.StatusTooManyRequests,
			"websocket handshake rate limit exceeded")
		return
	}

	slot, err := a.wsConns.Admit(
		wstunnel.Key{RouteKey: route.RouteKey, Instance: backend.InstanceName},
		wstunnel.ClientIP(r),
		wstunnel.Limits{
			Total:    a.wsOpts.MaxConnections,
			PerRoute: route.WebsocketMaxConnections,
			PerIP:    a.wsOpts.MaxConnectionsPerIP,
		},
	)
	if err != nil {
		a.rejectHandshake(w, r, route, backend, string(wstunnel.Reason(err)), http.StatusServiceUnavailable, err.Error())
		return
	}
	defer slot.Release()

	endpoint := metrics.NormalizeEndpoint(appRelativePath(route, r.URL.Path))
	rec := metrics.WrapResponseWriter(w)

	// idle is captured at hijack time so the close path can tell an idle
	// expiry apart from a peer hangup. Written and read on the goroutine
	// running ServeHTTP (ReverseProxy hijacks inline), so no synchronization.
	var idle *wstunnel.IdleConn
	hook := &wstunnel.HijackHook{ResponseWriter: rec}
	hook.OnHijack = func(conn net.Conn) net.Conn {
		// Reaching here means the backend answered 101 and the tunnel is real.
		wrapped := wstunnel.NewIdleConn(conn, route.WebsocketIdleTimeout)
		if ic, ok := wrapped.(*wstunnel.IdleConn); ok {
			idle = ic
		}
		slot.Attach(wrapped)

		// Counted at open, not at close: a tunnel can outlive the dashboard's
		// retention, and an operator asking "is anything connected right now"
		// must not have to wait for it to end to find out.
		metrics.WebsocketHandshakes.WithLabelValues(
			route.RouteKey, route.ServiceName, route.Env, backend.InstanceName, handshakeSuccess,
		).Inc()
		metrics.WebsocketConnections.WithLabelValues(
			route.RouteKey, route.ServiceName, route.Env, backend.InstanceName,
		).Inc()
		metrics.RequestsTotal.WithLabelValues(
			route.RouteKey, route.ServiceName, route.Env, backend.InstanceName, r.Method,
			metrics.StatusClass(http.StatusSwitchingProtocols), endpoint,
		).Inc()
		alog.Info(r.Context(), "websocket tunnel opened",
			zap.String("route_key", route.RouteKey),
			zap.String("service_name", route.ServiceName),
			zap.String("env", route.Env),
			zap.String("instance", backend.InstanceName),
			zap.String("endpoint", endpoint),
			zap.String("trace_id", traceID),
			zap.Duration("idle_timeout", route.WebsocketIdleTimeout),
		)
		return wrapped
	}

	// r.Context() is still honoured: net/http cancels it when the client
	// disconnects, which is a real end-of-tunnel signal. What is deliberately
	// absent is any deadline layered on top of it.
	proxy := a.buildProxy(target, route, backend, r, traceID, a.wsTransport)
	start := time.Now()
	proxy.ServeHTTP(hook, r)

	if !hook.Hijacked() {
		// The backend declined the upgrade (any non-101 response, or a
		// transport error the ErrorHandler turned into 502). Its status is the
		// client's answer; record it as a failed handshake.
		metrics.WebsocketHandshakes.WithLabelValues(
			route.RouteKey, route.ServiceName, route.Env, backend.InstanceName, handshakeRefused,
		).Inc()
		metrics.RequestsTotal.WithLabelValues(
			route.RouteKey, route.ServiceName, route.Env, backend.InstanceName, r.Method,
			metrics.StatusClass(rec.Status()), endpoint,
		).Inc()
		alog.L().Warn("websocket handshake refused by backend",
			zap.String("route_key", route.RouteKey),
			zap.String("backend", backend.URL),
			zap.Int("status", rec.Status()),
			zap.String("trace_id", traceID),
		)
		return
	}

	duration := time.Since(start)
	reason := disconnectPeerClosed
	switch {
	case slot.Forced():
		reason = disconnectDrain
	case idle != nil && idle.TimedOut():
		reason = disconnectIdleTimeout
	}
	metrics.WebsocketConnections.WithLabelValues(
		route.RouteKey, route.ServiceName, route.Env, backend.InstanceName,
	).Dec()
	metrics.WebsocketConnectionDuration.WithLabelValues(
		route.RouteKey, route.ServiceName, route.Env,
	).Observe(duration.Seconds())
	metrics.WebsocketDisconnects.WithLabelValues(
		route.RouteKey, route.ServiceName, route.Env, reason,
	).Inc()
	alog.L().Info("websocket tunnel closed",
		zap.String("route_key", route.RouteKey),
		zap.String("service_name", route.ServiceName),
		zap.String("env", route.Env),
		zap.String("instance", backend.InstanceName),
		zap.String("endpoint", endpoint),
		zap.String("trace_id", traceID),
		zap.String("reason", reason),
		zap.Duration("duration", duration),
	)
}

// rejectHandshake refuses an upgrade before it reaches the backend, recording
// it under the same handshake counter as a success so the dashboard shows one
// total with an outcome breakdown.
func (a *GatewayApplication) rejectHandshake(
	w http.ResponseWriter,
	r *http.Request,
	route service.RouteSnapshot,
	backend service.BackendSnapshot,
	result string,
	status int,
	message string,
) {
	if result == "" {
		result = "rejected"
	}
	metrics.WebsocketHandshakes.WithLabelValues(
		route.RouteKey, route.ServiceName, route.Env, backend.InstanceName, result,
	).Inc()
	metrics.RequestsTotal.WithLabelValues(
		route.RouteKey, route.ServiceName, route.Env, backend.InstanceName, r.Method,
		metrics.StatusClass(status), metrics.NormalizeEndpoint(appRelativePath(route, r.URL.Path)),
	).Inc()
	alog.L().Warn("websocket handshake rejected",
		zap.String("route_key", route.RouteKey),
		zap.String("service_name", route.ServiceName),
		zap.String("env", route.Env),
		zap.String("result", result),
		zap.String("client_ip", wstunnel.ClientIP(r)),
		zap.String("origin", r.Header.Get("Origin")),
	)
	http.Error(w, message, status)
}

// BeginWebSocketDrain stops accepting new tunnels. Called at the start of
// shutdown, before the HTTP listeners close, so in-flight tunnels keep running
// while new ones are turned away and retried elsewhere.
func (a *GatewayApplication) BeginWebSocketDrain() {
	a.wsConns.BeginDrain()
}

// DrainWebSockets waits out live tunnels and force-closes whatever remains when
// ctx expires, returning the number it had to force.
//
// This is not something http.Server.Shutdown can do for us: Shutdown documents
// that it does not attempt to close or wait for hijacked connections, and every
// established tunnel is hijacked. Without this the process would exit with
// tunnels still attached, cutting them with no drain window at all.
func (a *GatewayApplication) DrainWebSockets(ctx context.Context) int {
	if a.wsConns.Active() == 0 {
		return 0
	}
	alog.Info(ctx, "draining websocket tunnels", zap.Int("active", a.wsConns.Active()))
	return a.wsConns.Drain(ctx)
}

// WebSocketStats reports live tunnels per route and instance. The instance
// teardown pipeline polls it to know when a decommissioned instance has no
// tunnels left to lose.
func (a *GatewayApplication) WebSocketStats() []wstunnel.Stat {
	return a.wsConns.Stats()
}
