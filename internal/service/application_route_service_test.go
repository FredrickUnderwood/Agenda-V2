package service

import (
	"context"
	"errors"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"gorm.io/gorm"
)

func TestGatewayRoutesSurviveInstanceRemovalAndReleaseMatchOnlyOnDelete(t *testing.T) {
	svc, db := newDeleteTestService(t)
	app, running, stopped, route := seedApp(t, db)
	ctx := context.Background()
	// Decommission preserves the instance and the route configuration.
	if err := svc.SetInstanceDesiredState(ctx, running.ID, domain.RuntimeStateStopped); err != nil {
		t.Fatal(err)
	}
	routes, err := svc.ListGatewayRoutes(ctx, app.ID)
	if err != nil || len(routes) != 1 || routes[0].ID != route.ID {
		t.Fatalf("routes after stopping instance: %+v, %v", routes, err)
	}
	for _, target := range []*domain.ApplicationEnvTarget{running, stopped} {
		if err := svc.DeleteInstance(ctx, app.ID, target.ID); err != nil {
			t.Fatal(err)
		}
	}
	routes, err = svc.ListGatewayRoutes(ctx, app.ID)
	if err != nil || len(routes) != 1 || routes[0].Enabled {
		t.Fatalf("disabled route must remain visible without instances: %+v, %v", routes, err)
	}
	// This is the original test -> prod host/path conflict. Disable intentionally
	// keeps the reservation, even across environments of the same application.
	replacement := &domain.ApplicationGatewayRoute{
		ApplicationID: app.ID, Env: domain.EnvironmentTest, RouteKey: "replacement",
		Host: route.Host, PathPrefix: route.PathPrefix, Enabled: true,
	}
	target := &domain.ApplicationEnvTarget{ApplicationID: app.ID, Env: domain.EnvironmentTest}
	if err := svc.validateGatewayRoutes(ctx, target, []*domain.ApplicationGatewayRoute{replacement}); err == nil {
		t.Fatal("disabled route no longer reserves its host/path")
	}
	if err := svc.DeleteGatewayRoute(ctx, app.ID, route.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.validateGatewayRoutes(ctx, target, []*domain.ApplicationGatewayRoute{replacement}); err != nil {
		t.Fatalf("deleted route still blocks the match: %v", err)
	}
	if err := svc.routes.SyncByApplicationEnv(ctx, app.ID, domain.EnvironmentTest, []*domain.ApplicationGatewayRoute{replacement}); err != nil {
		t.Fatalf("database reservation was not released: %v", err)
	}
}

func TestGatewayRouteDisableKeepsBackendsDeleteRemovesThem(t *testing.T) {
	svc, db := newDeleteTestService(t)
	app, _, _, route := seedApp(t, db)
	ctx := context.Background()
	if err := svc.DisableGatewayRoute(ctx, app.ID, route.ID); err != nil {
		t.Fatal(err)
	}
	routes, err := svc.ListGatewayRoutes(ctx, app.ID)
	if err != nil || len(routes) != 1 || routes[0].Enabled || len(routes[0].Backends) != 2 {
		t.Fatalf("disable lost route configuration: %+v, %v", routes, err)
	}
	if err := svc.DeleteGatewayRoute(ctx, app.ID+1, route.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign application delete: %v", err)
	}
	if err := svc.DeleteGatewayRoute(ctx, app.ID, route.ID); err != nil {
		t.Fatal(err)
	}
	var pins int64
	if err := db.Model(&domain.ApplicationGatewayRouteBackend{}).Where("route_id = ?", route.ID).Count(&pins).Error; err != nil || pins != 0 {
		t.Fatalf("selected backends survived deletion: %d, %v", pins, err)
	}
	routes, err = svc.ListGatewayRoutes(ctx, app.ID)
	if err != nil || len(routes) != 0 {
		t.Fatalf("deleted route still listed: %+v, %v", routes, err)
	}
}
