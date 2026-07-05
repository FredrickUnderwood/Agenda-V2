package application

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/service"
	alog "github.com/FredrickUnderwood/agenda-v2/sdk/go/log"
	"go.uber.org/zap"
)

// ErrInstanceSelectDisabled is returned by Match when a caller supplies a
// pinned instance name but the matched route has instance selection disabled.
var ErrInstanceSelectDisabled = errors.New("instance selection is disabled for this route")

// ErrInstanceNotFound is returned by Match when a caller pins an instance
// name that has no matching enabled/healthy backend on the matched route.
var ErrInstanceNotFound = errors.New("pinned instance not found")

type GatewayApplication struct {
	routes          *service.RouteService
	refreshInterval time.Duration

	mu        sync.RWMutex
	snapshots []service.RouteSnapshot
	counters  map[string]int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewGatewayApplication(routes *service.RouteService, refreshInterval time.Duration) *GatewayApplication {
	if refreshInterval <= 0 {
		refreshInterval = 2 * time.Second
	}
	return &GatewayApplication{
		routes:          routes,
		refreshInterval: refreshInterval,
		counters:        make(map[string]int),
	}
}

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

	ctx, cancel := context.WithTimeout(r.Context(), route.Timeout)
	defer cancel()
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.URL.Path = RoutedPath(target.Path, route, r.URL.Path)
		req.URL.RawPath = ""
		req.URL.RawQuery = r.URL.RawQuery
		req.Host = target.Host
		req.Header.Set("X-Agenda-Route", route.RouteKey)
		req.Header.Set("X-Agenda-Service", route.ServiceName)
		req.Header.Set("X-Agenda-Env", route.Env)
		req.Header.Set("X-Agenda-Release", route.CurrentReleaseID)
		req.Header.Set("X-Agenda-Backend", backend.TargetKey)
		appendForwardedHeaders(req, r)
	}
	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		alog.L().Warn("proxy request failed",
			zap.String("route_key", route.RouteKey),
			zap.String("backend", backend.URL),
			zap.Error(err),
		)
		http.Error(rw, "bad gateway", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r.WithContext(ctx))
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
	return singleJoiningSlash(targetPath, path)
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
