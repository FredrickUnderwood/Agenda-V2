package repository

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/domain"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&domain.Route{}, &domain.Backend{}, &domain.RouteHistory{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestUpsertRoutePersistsUnhealthyBackend is the regression guard for the
// health-gating bug: a backend the control plane marks Healthy=false must be
// stored as false, not silently flipped to the column default true (the GORM
// zero-value-with-default omission). Uses a real DB (sqlite) because the bug
// lives in the ORM's INSERT column selection, which a fake repo can't exercise.
func TestUpsertRoutePersistsUnhealthyBackend(t *testing.T) {
	db := newTestDB(t)
	repo := NewRouteRepository(db)
	ctx := context.Background()

	route := domain.Route{
		RouteKey:         "r1",
		ServiceName:      "svc",
		Env:              "prod",
		Host:             "h",
		PathPrefix:       "/",
		CurrentReleaseID: "rel-1",
		Status:           domain.RouteStatusEnabled,
		TimeoutMs:        30000,
	}
	backends := []domain.Backend{
		{TargetKey: "healthy-one", InstanceName: "blue", URL: "http://n/1", Weight: 1, Enabled: true, Healthy: true},
		{TargetKey: "unhealthy-one", InstanceName: "green", URL: "http://n/2", Weight: 1, Enabled: true, Healthy: false},
	}

	if _, err := repo.UpsertRoute(ctx, route, backends, "op", "reason"); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := repo.GetRoute(ctx, "r1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	byInstance := map[string]domain.Backend{}
	for _, b := range got.Backends {
		byInstance[b.InstanceName] = b
	}
	if !byInstance["blue"].Healthy {
		t.Errorf("blue healthy = false, want true")
	}
	if byInstance["green"].Healthy {
		t.Errorf("green healthy = true, want false — explicit Healthy=false must persist, not fall back to the column default")
	}
}

// TestUpsertRoutePersistsDisabledBackend guards the same trap on Enabled, which
// carried the identical default:true tag.
func TestUpsertRoutePersistsDisabledBackend(t *testing.T) {
	db := newTestDB(t)
	repo := NewRouteRepository(db)
	ctx := context.Background()

	route := domain.Route{
		RouteKey: "r1", ServiceName: "svc", Env: "prod", Host: "h",
		PathPrefix: "/", CurrentReleaseID: "rel-1", Status: domain.RouteStatusEnabled, TimeoutMs: 30000,
	}
	backends := []domain.Backend{
		{TargetKey: "off", InstanceName: "blue", URL: "http://n/1", Weight: 1, Enabled: false, Healthy: true},
	}
	if _, err := repo.UpsertRoute(ctx, route, backends, "op", "reason"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := repo.GetRoute(ctx, "r1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Backends) != 1 || got.Backends[0].Enabled {
		t.Errorf("backend enabled = true, want false to persist")
	}
}
