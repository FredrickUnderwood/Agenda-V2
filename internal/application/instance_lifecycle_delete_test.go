package application

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

// TestDeleteInstance_RefusesRunningInstance pins the safety property: only a
// decommissioned instance may be deleted. Deleting a running one would orphan
// its containers (nothing left names them) and strand the gateway backends
// pointing at it.
//
// The check runs before the deploy lock is taken, so this exercises the real
// code path without needing Redis — and that ordering is itself deliberate: a
// request that is going to be refused shouldn't contend for the lock a deploy
// might be holding.
func TestDeleteInstance_RefusesRunningInstance(t *testing.T) {
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
	appSvc := service.NewApplicationService(
		repository.NewApplicationRepository(db),
		repository.NewApplicationTargetRepository(db),
		repository.NewApplicationGatewayRouteRepository(db),
		repository.NewApplicationGatewayRouteBackendRepository(db),
		repository.NewMachineRepository(db),
		repository.NewApplicationInstanceHealthRepository(db),
	)

	app := &domain.Application{Name: "agenda-example", RepoURL: "git@example.com:x.git", DeployMethod: domain.DeployMethodDocker}
	if err := db.Create(app).Error; err != nil {
		t.Fatalf("create app: %v", err)
	}
	target := &domain.ApplicationEnvTarget{
		ApplicationID: app.ID, Env: domain.EnvironmentProd, InstanceName: "default",
		MachineID: 1, Port: 18081, Enabled: true, DesiredState: domain.RuntimeStateRunning,
	}
	if err := db.Create(target).Error; err != nil {
		t.Fatalf("create target: %v", err)
	}

	lifecycle := &InstanceLifecycleApplication{appSvc: appSvc}
	err = lifecycle.DeleteInstance(context.Background(), app.ID, target.ID)
	if !errors.Is(err, ErrInstanceNotStopped) {
		t.Fatalf("err = %v, want ErrInstanceNotStopped", err)
	}

	var count int64
	db.Model(&domain.ApplicationEnvTarget{}).Where("id = ?", target.ID).Count(&count)
	if count != 1 {
		t.Fatal("a running instance was deleted")
	}
}
