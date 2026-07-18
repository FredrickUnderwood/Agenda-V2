package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/domain"
)

func boolPtr(b bool) *bool { return &b }

type fakeRouteRepository struct {
	routes []domain.Route
}

func (f fakeRouteRepository) LoadEnabledRoutes(context.Context) ([]domain.Route, error) {
	return f.routes, nil
}

func (f fakeRouteRepository) ListRoutes(context.Context) ([]domain.Route, error) {
	return nil, nil
}

func (f fakeRouteRepository) GetRoute(context.Context, string) (domain.Route, error) {
	return domain.Route{}, nil
}

func (f fakeRouteRepository) UpsertRoute(context.Context, domain.Route, []domain.Backend, string, string) (domain.Route, error) {
	return domain.Route{}, nil
}

func (f fakeRouteRepository) RollbackRoute(context.Context, string, string, string) (domain.Route, error) {
	return domain.Route{}, nil
}

func TestLoadSnapshotsSpecificHostBeforeWildcard(t *testing.T) {
	svc := NewRouteService(fakeRouteRepository{routes: []domain.Route{
		{
			RouteKey:         "wildcard",
			Host:             "*",
			PathPrefix:       "/",
			CurrentReleaseID: "r1",
			TimeoutMs:        1000,
			Backends: []domain.Backend{
				{TargetKey: "wildcard", URL: "http://127.0.0.1:1", Weight: 1, Enabled: true, Healthy: true},
			},
		},
		{
			RouteKey:         "specific",
			Host:             "api.example.com",
			PathPrefix:       "/",
			CurrentReleaseID: "r1",
			TimeoutMs:        1000,
			Backends: []domain.Backend{
				{TargetKey: "specific", URL: "http://127.0.0.1:2", Weight: 1, Enabled: true, Healthy: true},
			},
		},
	}}, 30*time.Second)
	snapshots, err := svc.LoadSnapshots(context.Background())
	if err != nil {
		t.Fatalf("LoadSnapshots error = %v", err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("len(snapshots) = %d, want 2", len(snapshots))
	}
	if snapshots[0].RouteKey != "specific" {
		t.Fatalf("first route = %s, want specific", snapshots[0].RouteKey)
	}
}

// TestUpsertRoundTripFromControlPlaneWire builds the request exactly as the
// control-plane sync step (internal/pipeline/step_gateway.go) does, sends it
// through a JSON marshal/unmarshal (the wire hop), then verifies the gateway
// normalizes it into a typed domain.Route. This guards the shared
// internal/contract types against wire drift and pins down the two reconciliation
// points: string Status -> typed enum, and *bool backend flags (nil defaults to
// true, but an explicit false must be preserved).
func TestUpsertRoundTripFromControlPlaneWire(t *testing.T) {
	// As the control plane builds it: string status, no route_key, no timeout_ms,
	// explicit *bool backend flags.
	sent := contract.UpsertRouteRequest{
		ApplicationID: 7,
		ServiceName:   "pay-service",
		Env:           "prod",
		Host:          "api.example.com",
		PathPrefix:    "/",
		ReleaseID:     "rel-123",
		Status:        "enabled",
		Operator:      "agenda-v2",
		Backends: []contract.BackendEntry{
			{TargetKey: "t1", URL: "http://10.0.0.1:8080", Weight: 2, Enabled: boolPtr(true), Healthy: boolPtr(true)},
			{TargetKey: "t2", URL: "http://10.0.0.2:8080", Weight: 1, Enabled: boolPtr(false), Healthy: boolPtr(true)},
		},
	}

	raw, err := json.Marshal(sent)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// omitempty fields the control plane leaves unset must not appear on the wire.
	if s := string(raw); strings.Contains(s, "route_key") || strings.Contains(s, "timeout_ms") || strings.Contains(s, "instance_select_mode") {
		t.Fatalf("unexpected omitempty field present on wire: %s", s)
	}

	var got contract.UpsertRouteRequest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got.RouteKey = "api-pay-service-prod" // the gateway sets this from the URL path

	svc := NewRouteService(fakeRouteRepository{}, 30*time.Second)
	route, backends, err := svc.normalizeUpsertRequest(got)
	if err != nil {
		t.Fatalf("normalizeUpsertRequest: %v", err)
	}

	if route.Status != domain.RouteStatusEnabled {
		t.Errorf("Status = %q, want %q", route.Status, domain.RouteStatusEnabled)
	}
	if route.InstanceSelectMode != domain.InstanceSelectModeDisabled {
		t.Errorf("InstanceSelectMode = %q, want disabled default", route.InstanceSelectMode)
	}
	if route.InstanceHeader != domain.DefaultInstanceHeader {
		t.Errorf("InstanceHeader = %q, want default %q", route.InstanceHeader, domain.DefaultInstanceHeader)
	}
	if route.TimeoutMs != 30000 {
		t.Errorf("TimeoutMs = %d, want default 30000", route.TimeoutMs)
	}
	if len(backends) != 2 {
		t.Fatalf("len(backends) = %d, want 2", len(backends))
	}
	if !backends[0].Enabled || !backends[0].Healthy {
		t.Errorf("backend[0] enabled=%v healthy=%v, want both true", backends[0].Enabled, backends[0].Healthy)
	}
	if backends[1].Enabled {
		t.Error("backend[1] enabled=true, want false — explicit *bool false must survive the round trip")
	}
}

// TestUnhealthyBackendExcludedFromSnapshot pins the health-gating contract end
// to end in current source: an explicit Healthy:false from the control-plane
// wire must survive normalization AND be dropped from the routing snapshot, so
// the round-robin pool never targets an instance the control plane marked
// unhealthy.
func TestUnhealthyBackendExcludedFromSnapshot(t *testing.T) {
	sent := contract.UpsertRouteRequest{
		ApplicationID: 1,
		ServiceName:   "agenda-example",
		Env:           "prod",
		Host:          "agenda-example.local",
		PathPrefix:    "/",
		ReleaseID:     "rel-1",
		Status:        "enabled",
		Backends: []contract.BackendEntry{
			{TargetKey: "blue", InstanceName: "blue", URL: "http://n:7200/i/blue", Weight: 1, Enabled: boolPtr(true), Healthy: boolPtr(true)},
			{TargetKey: "green", InstanceName: "green", URL: "http://n:7200/i/green", Weight: 1, Enabled: boolPtr(true), Healthy: boolPtr(false)},
		},
	}
	raw, err := json.Marshal(sent)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got contract.UpsertRouteRequest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got.RouteKey = "agenda-example-prod-default"

	svc := NewRouteService(fakeRouteRepository{}, 30*time.Second)
	route, backends, err := svc.normalizeUpsertRequest(got)
	if err != nil {
		t.Fatalf("normalizeUpsertRequest: %v", err)
	}
	if backends[0].Healthy != true || backends[1].Healthy != false {
		t.Fatalf("healthy round-trip wrong: blue=%v green=%v, want true/false", backends[0].Healthy, backends[1].Healthy)
	}

	route.Backends = backends
	snapshot := svc.toSnapshot(route)
	for _, b := range snapshot.Backends {
		if b.InstanceName == "green" {
			t.Fatalf("unhealthy backend 'green' leaked into routing snapshot: %+v", snapshot.Backends)
		}
	}
	if len(snapshot.Backends) != 1 || snapshot.Backends[0].InstanceName != "blue" {
		t.Fatalf("snapshot backends = %+v, want only healthy 'blue'", snapshot.Backends)
	}
}
