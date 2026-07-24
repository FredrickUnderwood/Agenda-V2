package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/gatewayclient"
	"github.com/FredrickUnderwood/agenda-v2/internal/nodeproxy"
)

type GatewayRouteSyncStep struct {
	Client        *gatewayclient.Client
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

	// ProxyKey is the app-scoped registry key (nodeproxy.ProxyKey) this backend
	// registers under on its node — also the /i/<key> segment of URL. Distinct
	// from InstanceName, which stays the bare name for the gateway's
	// instance-pinning (X-Agenda-Instance) semantics.
	ProxyKey string

	// Proxy* are set only for agent-mode backends. When ProxyAgentBaseURL is
	// non-empty the step first registers ProxyPort with the node's reverse proxy
	// under ProxyKey (so URL, which points at the node's stable /i/<key> path,
	// resolves to the instance's current local port) before syncing the route
	// to the gateway.
	ProxyAgentBaseURL string
	ProxyAgentToken   string
	ProxyPort         int
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
		backends := make([]contract.BackendEntry, 0, len(route.Backends))
		urls := make([]string, 0, len(route.Backends))
		for _, backend := range route.Backends {
			// Agent-mode backends: register the instance's current port with the
			// node's reverse proxy before pointing the gateway at the node's
			// stable proxy URL. A registration failure fails the step (same
			// all-or-nothing granularity as the route upsert itself).
			if backend.ProxyAgentBaseURL != "" {
				if err := nodeproxy.RegisterProxyTarget(ctx, backend.ProxyAgentBaseURL, backend.ProxyAgentToken, backend.ProxyKey, backend.ProxyPort); err != nil {
					return fmt.Errorf("register proxy target %q (instance %q): %w", backend.ProxyKey, backend.InstanceName, err)
				}
			}
			enabled := route.Enabled
			healthy := backend.Healthy
			backends = append(backends, contract.BackendEntry{
				TargetKey:    backend.TargetKey,
				InstanceName: backend.InstanceName,
				URL:          backend.URL,
				Weight:       backend.Weight,
				Enabled:      &enabled,
				Healthy:      &healthy,
			})
			urls = append(urls, fmt.Sprintf("%s[%s healthy=%t]", backend.URL, backend.InstanceName, healthy))
		}
		req := contract.UpsertRouteRequest{
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
