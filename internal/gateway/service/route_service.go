package service

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/domain"
	alog "github.com/FredrickUnderwood/agenda-v2/sdk/go/log"
	"go.uber.org/zap"
)

type RouteRepository interface {
	LoadEnabledRoutes(context.Context) ([]domain.Route, error)
	ListRoutes(context.Context) ([]domain.Route, error)
	GetRoute(context.Context, string) (domain.Route, error)
	UpsertRoute(context.Context, domain.Route, []domain.Backend, string, string) (domain.Route, error)
	RollbackRoute(context.Context, string, string, string) (domain.Route, error)
}

type RouteService struct {
	routes           RouteRepository
	defaultTimeoutMs int
}

func NewRouteService(routes RouteRepository, defaultTimeout time.Duration) *RouteService {
	timeoutMs := int(defaultTimeout / time.Millisecond)
	if timeoutMs <= 0 {
		timeoutMs = 30000
	}
	return &RouteService{routes: routes, defaultTimeoutMs: timeoutMs}
}

type RollbackRouteRequest struct {
	Operator string `json:"operator"`
	Reason   string `json:"reason"`
}

type RouteSnapshot struct {
	RouteKey           string
	ApplicationID      int64
	ServiceName        string
	Env                string
	Host               string
	PathPrefix         string
	StripPrefix        bool
	CurrentReleaseID   string
	InstanceSelectMode domain.InstanceSelectMode
	InstanceHeader     string
	Timeout            time.Duration
	Backends           []BackendSnapshot
}

type BackendSnapshot struct {
	TargetKey    string
	InstanceName string
	URL          string
}

func (s *RouteService) ListRoutes(ctx context.Context) ([]domain.Route, error) {
	return s.routes.ListRoutes(ctx)
}

func (s *RouteService) GetRoute(ctx context.Context, routeKey string) (domain.Route, error) {
	routeKey = strings.TrimSpace(routeKey)
	if routeKey == "" {
		return domain.Route{}, domain.NewInvalidParamError("route_key is required")
	}
	return s.routes.GetRoute(ctx, routeKey)
}

func (s *RouteService) UpsertRoute(ctx context.Context, routeKey string, req contract.UpsertRouteRequest) (domain.Route, error) {
	req.RouteKey = strings.TrimSpace(routeKey)
	route, backends, err := s.normalizeUpsertRequest(req)
	if err != nil {
		return domain.Route{}, err
	}
	out, err := s.routes.UpsertRoute(ctx, route, backends, req.Operator, req.Reason)
	if err != nil {
		return domain.Route{}, err
	}
	alog.Info(ctx, "route upserted",
		zap.String("route_key", out.RouteKey),
		zap.String("service_name", out.ServiceName),
		zap.String("env", out.Env),
		zap.String("release_id", out.CurrentReleaseID),
		zap.Int("backend_count", len(out.Backends)),
	)
	return out, nil
}

func (s *RouteService) RollbackRoute(ctx context.Context, routeKey string, req RollbackRouteRequest) (domain.Route, error) {
	routeKey = strings.TrimSpace(routeKey)
	if routeKey == "" {
		return domain.Route{}, domain.NewInvalidParamError("route_key is required")
	}
	out, err := s.routes.RollbackRoute(ctx, routeKey, req.Operator, req.Reason)
	if err != nil {
		return domain.Route{}, err
	}
	alog.Info(ctx, "route rollback requested",
		zap.String("route_key", out.RouteKey),
		zap.String("release_id", out.CurrentReleaseID),
	)
	return out, nil
}

func (s *RouteService) LoadSnapshots(ctx context.Context) ([]RouteSnapshot, error) {
	routes, err := s.routes.LoadEnabledRoutes(ctx)
	if err != nil {
		return nil, err
	}
	snapshots := make([]RouteSnapshot, 0, len(routes))
	for _, route := range routes {
		snapshot := s.toSnapshot(route)
		if len(snapshot.Backends) == 0 {
			continue
		}
		snapshots = append(snapshots, snapshot)
	}
	sort.SliceStable(snapshots, func(i, j int) bool {
		if snapshots[i].Host == "*" && snapshots[j].Host != "*" {
			return false
		}
		if snapshots[i].Host != "*" && snapshots[j].Host == "*" {
			return true
		}
		if snapshots[i].Host != snapshots[j].Host {
			return snapshots[i].Host < snapshots[j].Host
		}
		return len(snapshots[i].PathPrefix) > len(snapshots[j].PathPrefix)
	})
	return snapshots, nil
}

func (s *RouteService) normalizeUpsertRequest(req contract.UpsertRouteRequest) (domain.Route, []domain.Backend, error) {
	if req.RouteKey == "" {
		return domain.Route{}, nil, domain.NewInvalidParamError("route_key is required")
	}
	if strings.TrimSpace(req.ServiceName) == "" {
		return domain.Route{}, nil, domain.NewInvalidParamError("service_name is required")
	}
	if strings.TrimSpace(req.Env) == "" {
		return domain.Route{}, nil, domain.NewInvalidParamError("env is required")
	}
	if strings.TrimSpace(req.Host) == "" {
		return domain.Route{}, nil, domain.NewInvalidParamError("host is required")
	}
	if strings.TrimSpace(req.ReleaseID) == "" {
		return domain.Route{}, nil, domain.NewInvalidParamError("release_id is required")
	}
	if len(req.Backends) == 0 {
		return domain.Route{}, nil, domain.NewInvalidParamError("at least one backend is required")
	}
	status := domain.RouteStatus(req.Status)
	if status == "" {
		status = domain.RouteStatusEnabled
	}
	if status != domain.RouteStatusEnabled && status != domain.RouteStatusDisabled {
		return domain.Route{}, nil, domain.NewInvalidParamError("status must be enabled or disabled")
	}
	instanceSelectMode := domain.InstanceSelectMode(req.InstanceSelectMode)
	if instanceSelectMode == "" {
		instanceSelectMode = domain.InstanceSelectModeDisabled
	}
	if instanceSelectMode != domain.InstanceSelectModeDisabled && instanceSelectMode != domain.InstanceSelectModeEnabled {
		return domain.Route{}, nil, domain.NewInvalidParamError("instance_select_mode must be disabled or enabled")
	}
	instanceHeader := strings.TrimSpace(req.InstanceHeader)
	if instanceHeader == "" {
		instanceHeader = domain.DefaultInstanceHeader
	}
	pathPrefix := normalizePathPrefix(req.PathPrefix)
	timeoutMs := req.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = s.defaultTimeoutMs
	}
	route := domain.Route{
		RouteKey:           req.RouteKey,
		ApplicationID:      req.ApplicationID,
		ServiceName:        strings.TrimSpace(req.ServiceName),
		Env:                strings.TrimSpace(req.Env),
		Host:               normalizeHost(req.Host),
		PathPrefix:         pathPrefix,
		StripPrefix:        req.StripPrefix,
		CurrentReleaseID:   strings.TrimSpace(req.ReleaseID),
		Status:             status,
		InstanceSelectMode: instanceSelectMode,
		InstanceHeader:     instanceHeader,
		TimeoutMs:          timeoutMs,
	}
	backends := make([]domain.Backend, 0, len(req.Backends))
	for _, item := range req.Backends {
		backend, err := normalizeBackend(item)
		if err != nil {
			return domain.Route{}, nil, err
		}
		backends = append(backends, backend)
	}
	return route, backends, nil
}

func (s *RouteService) toSnapshot(route domain.Route) RouteSnapshot {
	timeout := time.Duration(route.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = time.Duration(s.defaultTimeoutMs) * time.Millisecond
	}
	snapshot := RouteSnapshot{
		RouteKey:           route.RouteKey,
		ApplicationID:      route.ApplicationID,
		ServiceName:        route.ServiceName,
		Env:                route.Env,
		Host:               normalizeHost(route.Host),
		PathPrefix:         normalizePathPrefix(route.PathPrefix),
		StripPrefix:        route.StripPrefix,
		CurrentReleaseID:   route.CurrentReleaseID,
		InstanceSelectMode: route.InstanceSelectMode,
		InstanceHeader:     route.InstanceHeader,
		Timeout:            timeout,
	}
	for _, backend := range route.Backends {
		if !backend.Enabled || !backend.Healthy {
			continue
		}
		weight := backend.Weight
		if weight <= 0 {
			weight = 1
		}
		if weight > 1000 {
			weight = 1000
		}
		for i := 0; i < weight; i++ {
			snapshot.Backends = append(snapshot.Backends, BackendSnapshot{
				TargetKey:    backend.TargetKey,
				InstanceName: backend.InstanceName,
				URL:          backend.URL,
			})
		}
	}
	return snapshot
}

func normalizeBackend(req contract.BackendEntry) (domain.Backend, error) {
	targetKey := strings.TrimSpace(req.TargetKey)
	if targetKey == "" {
		return domain.Backend{}, domain.NewInvalidParamError("backend target_key is required")
	}
	rawURL := strings.TrimSpace(req.URL)
	if rawURL == "" {
		return domain.Backend{}, domain.NewInvalidParamError("backend " + targetKey + " url is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return domain.Backend{}, domain.NewInvalidParamError("backend " + targetKey + " url is invalid")
	}
	weight := req.Weight
	if weight <= 0 {
		weight = 1
	}
	if weight > 1000 {
		return domain.Backend{}, domain.NewInvalidParamError("backend " + targetKey + " weight must be <= 1000")
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	healthy := true
	if req.Healthy != nil {
		healthy = *req.Healthy
	}
	return domain.Backend{
		TargetKey:    targetKey,
		InstanceName: strings.TrimSpace(req.InstanceName),
		URL:          rawURL,
		Weight:       weight,
		Enabled:      enabled,
		Healthy:      healthy,
	}, nil
}

func normalizePathPrefix(pathPrefix string) string {
	pathPrefix = strings.TrimSpace(pathPrefix)
	if pathPrefix == "" {
		return "/"
	}
	if !strings.HasPrefix(pathPrefix, "/") {
		pathPrefix = "/" + pathPrefix
	}
	if len(pathPrefix) > 1 {
		pathPrefix = strings.TrimRight(pathPrefix, "/")
		if pathPrefix == "" {
			return "/"
		}
	}
	return pathPrefix
}

func normalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if i := strings.IndexByte(host, ':'); i >= 0 {
		return host[:i]
	}
	return host
}
