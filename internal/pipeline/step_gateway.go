package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/agenda-v2/internal/gateway"
)

type GatewayRouteSyncStep struct {
	Client        *gateway.Client
	ApplicationID int64
	ServiceName   string
	Env           string
	Routes        []GatewayRouteSpec
}

// GatewayRouteSpec describes one gateway route to sync and the backend(s)
// resolved for it — one backend for backend_mode=single (the currently
// deploying instance), N for all_enabled/selected.
type GatewayRouteSpec struct {
	RouteKey           string
	Host               string
	PathPrefix         string
	StripPrefix        bool
	Enabled            bool
	InstanceSelectMode string
	InstanceHeader     string
	Backends           []GatewayBackendSpec
}

type GatewayBackendSpec struct {
	InstanceName string
	TargetKey    string
	URL          string
	Weight       int
	Healthy      bool
}

func (s *GatewayRouteSyncStep) Execute(ctx context.Context, rc *RunContext) error {
	releaseID := rc.Log.TriggerSHA
	if releaseID == "" {
		releaseID = rc.Branch
	}
	for _, route := range s.Routes {
		status := "disabled"
		if route.Enabled {
			status = "enabled"
		}
		backends := make([]gateway.BackendEntry, 0, len(route.Backends))
		urls := make([]string, 0, len(route.Backends))
		for _, backend := range route.Backends {
			backends = append(backends, gateway.BackendEntry{
				TargetKey:    backend.TargetKey,
				InstanceName: backend.InstanceName,
				URL:          backend.URL,
				Weight:       backend.Weight,
				Enabled:      route.Enabled,
				Healthy:      backend.Healthy,
			})
			urls = append(urls, backend.URL)
		}
		req := gateway.UpsertRouteRequest{
			ApplicationID:      s.ApplicationID,
			ServiceName:        s.ServiceName,
			Env:                s.Env,
			Host:               route.Host,
			PathPrefix:         route.PathPrefix,
			StripPrefix:        route.StripPrefix,
			ReleaseID:          releaseID,
			Status:             status,
			InstanceSelectMode: route.InstanceSelectMode,
			InstanceHeader:     route.InstanceHeader,
			Operator:           "agenda-v2",
			Reason:             fmt.Sprintf("deploy log %d succeeded", rc.Log.ID),
			Backends:           backends,
		}
		_, _ = fmt.Fprintf(rc.Output, "sync gateway route %q (%s) -> %s\n", route.RouteKey, status, strings.Join(urls, ", "))
		if err := s.Client.UpsertRoute(ctx, route.RouteKey, req); err != nil {
			return err
		}
	}
	return nil
}
