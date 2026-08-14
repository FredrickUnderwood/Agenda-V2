package application

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/metrics"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/service"
	"github.com/FredrickUnderwood/agenda-v2/internal/wstunnel"
	alog "github.com/FredrickUnderwood/agenda-v2/sdk/go/log"
	"go.uber.org/zap"
)

// ErrInstanceSelectDisabled is returned by Match when a caller supplies a
// pinned instance name but the matched route has instance selection disabled.
var ErrInstanceSelectDisabled = errors.New("instance selection is disabled for this route")

// ErrInstanceNotFound is returned by Match when a caller pins an instance
// name that has no matching enabled/healthy backend on the matched route.
var ErrInstanceNotFound = errors.New("pinned instance not found")

// WebSocketOptions are the process-wide WebSocket knobs, complementing the
// per-route ones carried on a RouteSnapshot. They exist because a route's
// author can't reason about the gateway's total capacity: one tunnel costs a
// connection on the gateway AND one on the node relay in front of the app, so
// the ceiling belongs to whoever sized the gateway.
type WebSocketOptions struct {
	// MaxConnections caps established tunnels across all routes (0 = unlimited).
	MaxConnections int
	// MaxConnectionsPerIP caps tunnels from one peer address (0 = unlimited).
	// See wstunnel.ClientIP for what "one peer" means behind a load balancer.
	MaxConnectionsPerIP int
	// HandshakeRate/HandshakeBurst throttle handshakes per second across the
	// gateway (0 = unthrottled). A reconnect storm after a deploy is cheap to
	// start and expensive to serve, so it is rate-limited separately from the
	// connection count.
	HandshakeRate  float64
	HandshakeBurst int
	// DialTimeout and ResponseHeaderTimeout bound the handshake itself. They
	// matter more than usual here: an upgrade request runs without the route's
	// total request timeout, so these are the only thing standing between a
	// black-holed backend and a permanently stuck handshake.
	DialTimeout           time.Duration
	ResponseHeaderTimeout time.Duration
}

func (o WebSocketOptions) withDefaults() WebSocketOptions {
	if o.DialTimeout <= 0 {
		o.DialTimeout = 5 * time.Second
	}
	if o.ResponseHeaderTimeout <= 0 {
		o.ResponseHeaderTimeout = 10 * time.Second
	}
	return o
}

type GatewayApplication struct {
	routes          *service.RouteService
	refreshInterval time.Duration

	wsOpts    WebSocketOptions
	wsConns   *wstunnel.Registry
	wsLimiter *wstunnel.RateLimiter

	// Transports are built once and shared. Before, every proxied request built
	// a fresh ReverseProxy on http.DefaultTransport; the ReverseProxy is still
	// per-request (its Director closes over the route), but the transport —
	// which owns the connection pool — must not be.
	httpTransport http.RoundTripper
	wsTransport   http.RoundTripper

	mu        sync.RWMutex
	snapshots []service.RouteSnapshot
	counters  map[string]int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewGatewayApplication(routes *service.RouteService, refreshInterval time.Duration, wsOpts WebSocketOptions) *GatewayApplication {
	if refreshInterval <= 0 {
		refreshInterval = 2 * time.Second
	}
	wsOpts = wsOpts.withDefaults()
	return &GatewayApplication{
		routes:          routes,
		refreshInterval: refreshInterval,
		wsOpts:          wsOpts,
		wsConns:         wstunnel.NewRegistry(),
		wsLimiter:       wstunnel.NewRateLimiter(wsOpts.HandshakeRate, wsOpts.HandshakeBurst),
		httpTransport:   newHTTPTransport(),
		wsTransport:     newWebSocketTransport(wsOpts),
		counters:        make(map[string]int),
	}
}

// newHTTPTransport is DefaultTransport's configuration, owned by this gateway
// so tuning it never leaks into unrelated http.Get callers in-process.
func newHTTPTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

// newWebSocketTransport is pinned to HTTP/1.1: RFC 6455 upgrades only exist
// there, and a transport that negotiated h2 with a TLS backend would fail the
// handshake instead of tunneling it. Idle connections are not pooled — an
// upgraded connection is consumed by the tunnel and never reusable.
func newWebSocketTransport(opts WebSocketOptions) *http.Transport {
	return &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: opts.DialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:   false,
		TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
		TLSHandshakeTimeout: opts.DialTimeout,
		// Bounds "TCP connected, then silence" — the failure mode that used to
		// be caught by the route's total request timeout.
		ResponseHeaderTimeout: opts.ResponseHeaderTimeout,
		DisableKeepAlives:     true,
	}
}

// WebSocketRegistry exposes the live-tunnel registry for the admin stats
// endpoint and for shutdown draining.
func (a *GatewayApplication) WebSocketRegistry() *wstunnel.Registry { return a.wsConns }

func (a *GatewayApplication) Start(ctx context.Context) error {
	a.ctx, a.cancel = context.WithCancel(ctx)
	if err := a.Refresh(ctx); err != nil {
		return err
	}
	a.wg.Add(1)
	go a.refreshLoop()
	return nil
}

func (a *GatewayApplication) Stop() {
	if a.cancel != nil {
		a.cancel()
	}
	a.wg.Wait()
}

func (a *GatewayApplication) Refresh(ctx context.Context) error {
	snapshots, err := a.routes.LoadSnapshots(ctx)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.snapshots = snapshots
	a.mu.Unlock()
	alog.Info(ctx, "gateway route cache refreshed", zap.Int("route_count", len(snapshots)))
	return nil
}

func (a *GatewayApplication) ServeProxy(w http.ResponseWriter, r *http.Request, pinnedInstance string) {
	route, backend, ok, err := a.Match(r.Host, r.URL.Path, pinnedInstance)
	if err != nil {
		switch {
		case errors.Is(err, ErrInstanceSelectDisabled):
			http.Error(w, "instance selection is disabled for this route", http.StatusBadRequest)
		case errors.Is(err, ErrInstanceNotFound):
			http.Error(w, "pinned instance not found", http.StatusNotFound)
		default:
			http.Error(w, "gateway route not found", http.StatusNotFound)
		}
		return
	}
	if !ok {
		http.Error(w, "gateway route not found", http.StatusNotFound)
		return
	}
	target, err := url.Parse(backend.URL)
	if err != nil {
		alog.L().Error("backend url parse failed",
			zap.String("route_key", route.RouteKey),
			zap.String("backend", backend.URL),
			zap.Error(err),
		)
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}

	// Trace propagation: reuse the caller's trace id (an upstream agenda service
	// or a client that set one) or mint a fresh one, so this request is
	// correlatable across the gateway and the backend's own logs. It's forwarded
	// to the backend on the request and echoed on the response. Matches
	// sdk/go/log.TraceHeader so a backend using ginlog.Middleware logs the same id.
	traceID := r.Header.Get(alog.TraceHeader)
	if traceID == "" {
		traceID = alog.NewTraceID()
	}

	if proto := wstunnel.UpgradeProtocol(r); proto != "" {
		a.serveUpgrade(w, r, route, backend, target, traceID, proto)
		return
	}
	a.serveRequest(w, r, route, backend, target, traceID)
}

// serveRequest is the ordinary HTTP path: one request, one response, bounded by
// the route's total timeout.
func (a *GatewayApplication) serveRequest(
	w http.ResponseWriter,
	r *http.Request,
	route service.RouteSnapshot,
	backend service.BackendSnapshot,
	target *url.URL,
	traceID string,
) {
	ctx, cancel := context.WithTimeout(r.Context(), route.Timeout)
	defer cancel()

	proxy := a.buildProxy(target, route, backend, r, traceID, a.httpTransport)
	rec := metrics.WrapResponseWriter(w)
	start := time.Now()
	proxy.ServeHTTP(rec, r.WithContext(ctx))

	// endpoint is the app-relative path (prefix stripped when the route strips
	// it), normalized to bounded cardinality — so the same app endpoint reads
	// the same whether reached via an external or internal route.
	endpoint := metrics.NormalizeEndpoint(appRelativePath(route, r.URL.Path))
	metrics.RequestsTotal.WithLabelValues(
		route.RouteKey, route.ServiceName, route.Env, backend.InstanceName, r.Method, metrics.StatusClass(rec.Status()), endpoint,
	).Inc()
	metrics.RequestDuration.WithLabelValues(
		route.RouteKey, route.ServiceName, route.Env, r.Method, endpoint,
	).Observe(time.Since(start).Seconds())
}

// buildProxy assembles the ReverseProxy both paths use. Everything about how a
// request is presented to the backend — path rewriting, agenda headers, trace
// propagation, X-Forwarded-* — lives here exactly once, so the WebSocket
// handshake reaches the app looking like any other request. Only the transport
// differs.
func (a *GatewayApplication) buildProxy(
	target *url.URL,
	route service.RouteSnapshot,
	backend service.BackendSnapshot,
	original *http.Request,
	traceID string,
	transport http.RoundTripper,
) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = transport
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.URL.Path = RoutedPath(target.Path, route, original.URL.Path)
		req.URL.RawPath = ""
		req.URL.RawQuery = original.URL.RawQuery
		req.Host = target.Host
		req.Header.Set("X-Agenda-Route", route.RouteKey)
		req.Header.Set("X-Agenda-Service", route.ServiceName)
		req.Header.Set("X-Agenda-Env", route.Env)
		req.Header.Set("X-Agenda-Release", route.CurrentReleaseID)
		req.Header.Set("X-Agenda-Backend", backend.TargetKey)
		req.Header.Set(alog.TraceHeader, traceID)
		appendForwardedHeaders(req, original)
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Set (not add) so a backend echoing the header doesn't duplicate it.
		resp.Header.Set(alog.TraceHeader, traceID)
		return nil
	}
	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		alog.L().Warn("proxy request failed",
			zap.String("route_key", route.RouteKey),
			zap.String("backend", backend.URL),
			zap.String("trace_id", traceID),
			zap.Error(err),
		)
		http.Error(rw, "bad gateway", http.StatusBadGateway)
	}
	return proxy
}

// Match finds the route for host/path and picks a backend. When
// pinnedInstance is non-empty, it looks for a backend with that exact
// InstanceName instead of round-robining, gated by the route's
// InstanceSelectMode (callers are expected to have already enforced auth for
// pinning before calling this).
func (a *GatewayApplication) Match(host, path, pinnedInstance string) (service.RouteSnapshot, service.BackendSnapshot, bool, error) {
	host = requestHost(host)
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, route := range a.snapshots {
		if route.Host != "*" && !strings.EqualFold(route.Host, host) {
			continue
		}
		if !PathMatches(path, route.PathPrefix) {
			continue
		}
		if len(route.Backends) == 0 {
			return service.RouteSnapshot{}, service.BackendSnapshot{}, false, nil
		}
		if pinnedInstance != "" {
			if route.InstanceSelectMode != domain.InstanceSelectModeEnabled {
				return service.RouteSnapshot{}, service.BackendSnapshot{}, false, ErrInstanceSelectDisabled
			}
			for _, backend := range route.Backends {
				if backend.InstanceName == pinnedInstance {
					return route, backend, true, nil
				}
			}
			return service.RouteSnapshot{}, service.BackendSnapshot{}, false, ErrInstanceNotFound
		}
		idx := a.counters[route.RouteKey] % len(route.Backends)
		a.counters[route.RouteKey]++
		return route, route.Backends[idx], true, nil
	}
	return service.RouteSnapshot{}, service.BackendSnapshot{}, false, nil
}

// LookupRouteConfig does a host/path-only match (no backend selection) to
// fetch a route's instance-pin configuration, so callers can decide whether
// to enforce auth for a pin header before touching the round-robin counter.
func (a *GatewayApplication) LookupRouteConfig(host, path string) (instanceSelectMode domain.InstanceSelectMode, instanceHeader string, found bool) {
	host = requestHost(host)
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, route := range a.snapshots {
		if route.Host != "*" && !strings.EqualFold(route.Host, host) {
			continue
		}
		if !PathMatches(path, route.PathPrefix) {
			continue
		}
		return route.InstanceSelectMode, route.InstanceHeader, true
	}
	return "", "", false
}

// Hosts returns the distinct, non-wildcard hostnames across all cached routes.
// The edge-TLS manager polls this to decide which domains need certificates.
func (a *GatewayApplication) Hosts() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	seen := make(map[string]struct{}, len(a.snapshots))
	hosts := make([]string, 0, len(a.snapshots))
	for _, route := range a.snapshots {
		if route.Host == "" || route.Host == "*" {
			continue
		}
		if _, ok := seen[route.Host]; ok {
			continue
		}
		seen[route.Host] = struct{}{}
		hosts = append(hosts, route.Host)
	}
	return hosts
}

func (a *GatewayApplication) refreshLoop() {
	defer a.wg.Done()
	ticker := time.NewTicker(a.refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := a.Refresh(ctx); err != nil {
				alog.L().Warn("refresh gateway routes failed", zap.Error(err))
			}
			cancel()
		}
	}
}

func PathMatches(path, prefix string) bool {
	if prefix == "/" {
		return true
	}
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	return len(path) == len(prefix) || path[len(prefix)] == '/'
}

func RoutedPath(targetPath string, route service.RouteSnapshot, originalPath string) string {
	return singleJoiningSlash(targetPath, appRelativePath(route, originalPath))
}

// appRelativePath is the request path as the backend app sees it: the incoming
// path with the route's prefix stripped when the route strips it (otherwise the
// app receives the prefix too, so it stays). This is the semantic "endpoint"
// used for per-endpoint metrics, independent of the backend base path.
func appRelativePath(route service.RouteSnapshot, originalPath string) string {
	path := originalPath
	if route.StripPrefix && route.PathPrefix != "/" {
		path = strings.TrimPrefix(path, route.PathPrefix)
		if path == "" {
			path = "/"
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
	}
	return path
}

func requestHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	if i := strings.IndexByte(host, ':'); i >= 0 {
		return host[:i]
	}
	return host
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	default:
		return a + b
	}
}

func appendForwardedHeaders(req *http.Request, original *http.Request) {
	host, _, err := net.SplitHostPort(original.RemoteAddr)
	if err != nil {
		host = original.RemoteAddr
	}
	if prior := req.Header.Get("X-Forwarded-For"); prior != "" {
		req.Header.Set("X-Forwarded-For", prior+", "+host)
	} else {
		req.Header.Set("X-Forwarded-For", host)
	}
	req.Header.Set("X-Forwarded-Host", original.Host)
	if original.TLS != nil {
		req.Header.Set("X-Forwarded-Proto", "https")
	} else if proto := original.Header.Get("X-Forwarded-Proto"); proto != "" {
		req.Header.Set("X-Forwarded-Proto", proto)
	} else {
		req.Header.Set("X-Forwarded-Proto", "http")
	}
}
