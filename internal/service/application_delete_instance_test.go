package service

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
)

// Uses a real (in-memory) DB rather than fakes: what DeleteInstance has to get
// right is which ROWS survive across five tables, which a fake repo would
// simply assert back at itself.
func newDeleteTestService(t *testing.T) (*ApplicationService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&domain.Application{},
		&domain.ApplicationEnvTarget{},
		&domain.ApplicationGatewayRoute{},
		&domain.ApplicationGatewayRouteBackend{},
		&domain.ApplicationInstanceHealth{},
		&domain.Machine{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := NewApplicationService(
		repository.NewApplicationRepository(db),
		repository.NewApplicationTargetRepository(db),
		repository.NewApplicationGatewayRouteRepository(db),
		repository.NewApplicationGatewayRouteBackendRepository(db),
		repository.NewMachineRepository(db),
		repository.NewApplicationInstanceHealthRepository(db),
	)
	return svc, db
}

// seedApp creates one app with a running "default" and a stopped "green"
// instance in prod, a route pinned to both, and a health row for each.
func seedApp(t *testing.T, db *gorm.DB) (app *domain.Application, running, stopped *domain.ApplicationEnvTarget, route *domain.ApplicationGatewayRoute) {
	t.Helper()
	app = &domain.Application{Name: "agenda-example", RepoURL: "git@example.com:x.git", DeployMethod: domain.DeployMethodDocker}
	if err := db.Create(app).Error; err != nil {
		t.Fatalf("create app: %v", err)
	}
	running = &domain.ApplicationEnvTarget{
		ApplicationID: app.ID, Env: domain.EnvironmentProd, InstanceName: "default",
		MachineID: 1, Port: 18081, Enabled: true, DesiredState: domain.RuntimeStateRunning,
	}
	stopped = &domain.ApplicationEnvTarget{
		ApplicationID: app.ID, Env: domain.EnvironmentProd, InstanceName: "green",
		MachineID: 2, Port: 18081, Enabled: true, DesiredState: domain.RuntimeStateStopped,
	}
	if err := db.Create(running).Error; err != nil {
		t.Fatalf("create running target: %v", err)
	}
	if err := db.Create(stopped).Error; err != nil {
		t.Fatalf("create stopped target: %v", err)
	}
	route = &domain.ApplicationGatewayRoute{
		ApplicationID: app.ID, Env: domain.EnvironmentProd, RouteKey: "agenda-example-prod",
		Host: "example.local", PathPrefix: "/", Enabled: true,
		BackendMode: domain.GatewayBackendModeSelected,
	}
	if err := db.Create(route).Error; err != nil {
		t.Fatalf("create route: %v", err)
	}
	for _, targetID := range []int64{running.ID, stopped.ID} {
		if err := db.Create(&domain.ApplicationGatewayRouteBackend{
			RouteID: route.ID, TargetID: targetID, Weight: 1, Enabled: true,
		}).Error; err != nil {
			t.Fatalf("create route backend: %v", err)
		}
		if err := db.Create(&domain.ApplicationInstanceHealth{
			TargetID: targetID, Status: "healthy",
		}).Error; err != nil {
			t.Fatalf("create health: %v", err)
		}
	}
	return app, running, stopped, route
}

func TestDeleteInstance_RemovesTargetAndItsReferences(t *testing.T) {
	svc, db := newDeleteTestService(t)
	app, running, stopped, route := seedApp(t, db)

	if err := svc.DeleteInstance(context.Background(), app.ID, stopped.ID); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	var targets int64
	db.Model(&domain.ApplicationEnvTarget{}).Where("id = ?", stopped.ID).Count(&targets)
	if targets != 0 {
		t.Error("the instance row survived the delete")
	}

	// The route pin is the one that actually breaks things if left behind: a
	// stale target_id makes every later application save fail validation.
	var pins int64
	db.Model(&domain.ApplicationGatewayRouteBackend{}).Where("target_id = ?", stopped.ID).Count(&pins)
	if pins != 0 {
		t.Errorf("gateway route pins for the deleted instance survived (%d)", pins)
	}

	var health int64
	db.Model(&domain.ApplicationInstanceHealth{}).Where("target_id = ?", stopped.ID).Count(&health)
	if health != 0 {
		t.Error("health row for the deleted instance survived")
	}

	// Everything belonging to the surviving sibling must be untouched.
	var siblingPins int64
	db.Model(&domain.ApplicationGatewayRouteBackend{}).Where("target_id = ?", running.ID).Count(&siblingPins)
	if siblingPins != 1 {
		t.Errorf("sibling's route pin count = %d, want 1", siblingPins)
	}
	var siblingHealth int64
	db.Model(&domain.ApplicationInstanceHealth{}).Where("target_id = ?", running.ID).Count(&siblingHealth)
	if siblingHealth != 1 {
		t.Errorf("sibling's health row count = %d, want 1", siblingHealth)
	}
	var got domain.ApplicationGatewayRoute
	if err := db.First(&got, route.ID).Error; err != nil {
		t.Fatalf("route disappeared: %v", err)
	}
	if !got.Enabled {
		t.Error("route was disabled even though the env still has an instance")
	}
}

// Deleting the last instance of an env leaves its routes with nothing to
// resolve backends from, so they are disabled — but kept, so the host/path
// config survives for the next instance deployed into that env.
func TestDeleteInstance_LastInEnvDisablesRoutes(t *testing.T) {
	svc, db := newDeleteTestService(t)
	app, running, stopped, route := seedApp(t, db)

	if err := svc.DeleteInstance(context.Background(), app.ID, stopped.ID); err != nil {
		t.Fatalf("delete first: %v", err)
	}
	// Mark the survivor stopped, then delete it too.
	if err := db.Model(&domain.ApplicationEnvTarget{}).Where("id = ?", running.ID).
		Update("desired_state", domain.RuntimeStateStopped).Error; err != nil {
		t.Fatalf("stop survivor: %v", err)
	}
	if err := svc.DeleteInstance(context.Background(), app.ID, running.ID); err != nil {
		t.Fatalf("delete last: %v", err)
	}

	var got domain.ApplicationGatewayRoute
	if err := db.First(&got, route.ID).Error; err != nil {
		t.Fatalf("route row was deleted, want it kept-but-disabled: %v", err)
	}
	if got.Enabled {
		t.Error("route is still enabled after its env lost every instance")
	}
	if got.Host != "example.local" || got.PathPrefix != "/" {
		t.Errorf("route config was not preserved: host=%q path=%q", got.Host, got.PathPrefix)
	}
}

// An instance id belonging to a different application must not be deletable
// through another app's URL.
func TestDeleteInstance_RejectsForeignInstance(t *testing.T) {
	svc, db := newDeleteTestService(t)
	app, _, stopped, _ := seedApp(t, db)

	other := &domain.Application{Name: "other", RepoURL: "git@example.com:o.git", DeployMethod: domain.DeployMethodDocker}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("create other app: %v", err)
	}
	if err := svc.DeleteInstance(context.Background(), other.ID, stopped.ID); err == nil {
		t.Fatal("deleted an instance through an unrelated application's id")
	}
	var count int64
	db.Model(&domain.ApplicationEnvTarget{}).Where("id = ?", stopped.ID).Count(&count)
	if count != 1 {
		t.Error("the instance was removed despite the rejection")
	}
	_ = app
}
