package application

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type routeTestGateway struct {
	err   error
	calls int
}

func (g *routeTestGateway) DeleteRoute(context.Context, string, contract.RouteOwner) error {
	g.calls++
	return g.err
}
func (g *routeTestGateway) DisableRoute(context.Context, string, contract.RouteOwner) error {
	g.calls++
	return g.err
}

type routeTestLocks struct {
	held map[string]bool
	busy string
}

func (l *routeTestLocks) AcquireKey(_ context.Context, key string, _ time.Duration) (string, error) {
	if key == l.busy {
		return "", service.ErrDeployLocked
	}
	l.held[key] = true
	return "token", nil
}
func (l *routeTestLocks) ReleaseKey(_ context.Context, key, _ string) { delete(l.held, key) }

func TestApplicationRouteLifecycleGatewayFailureAndRetry(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "disable", true: "delete"}[remove], func(t *testing.T) {
			a, svc, route, gateway, _ := newRouteApplicationTest(t)
			ctx := context.Background()
			change := a.Disable
			if remove {
				change = a.Delete
			}
			gateway.err = errors.New("gateway unavailable")
			if err := change(ctx, route.ApplicationID, route.ID); !errors.Is(err, gateway.err) {
				t.Fatalf("gateway failure was swallowed: %v", err)
			}
			got, err := svc.GetGatewayRoute(ctx, route.ApplicationID, route.ID)
			if err != nil || !got.Enabled {
				t.Fatalf("local reservation changed before gateway acknowledgment: %+v, %v", got, err)
			}
			gateway.err = nil
			if err := change(ctx, route.ApplicationID, route.ID); err != nil {
				t.Fatal(err)
			}
			got, err = svc.GetGatewayRoute(ctx, route.ApplicationID, route.ID)
			if remove {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Fatalf("route survived deletion: %+v, %v", got, err)
				}
			} else if err != nil || got.Enabled {
				t.Fatalf("route was not disabled: %+v, %v", got, err)
			}
		})
	}
}

func TestApplicationRouteDeleteChecksOwnershipAndDeployLocks(t *testing.T) {
	a, svc, route, gateway, db := newRouteApplicationTest(t)
	ctx := context.Background()
	if err := a.Delete(ctx, route.ApplicationID+1, route.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign route delete: %v", err)
	}
	locks := a.locks.(*routeTestLocks)
	locks.busy = "gateway-route:" + strconv.FormatInt(route.ID, 10)
	if err := a.Delete(ctx, route.ApplicationID, route.ID); !errors.Is(err, service.ErrDeployLocked) {
		t.Fatalf("route without instances must still serialize lifecycle requests: %v", err)
	}
	for _, name := range []string{"blue", "green"} {
		if err := db.Create(&domain.ApplicationEnvTarget{ApplicationID: route.ApplicationID, Env: route.Env, InstanceName: name, Enabled: true, Port: 8080}).Error; err != nil {
			t.Fatal(err)
		}
	}
	locks.busy = service.ReleaseLockKey(route.ApplicationID, string(route.Env), "green")
	if err := a.Delete(ctx, route.ApplicationID, route.ID); !errors.Is(err, service.ErrDeployLocked) {
		t.Fatalf("expected active deployment conflict: %v", err)
	}
	if len(locks.held) != 0 || gateway.calls != 0 {
		t.Fatalf("leaked locks or mutated gateway: %+v, %d", locks.held, gateway.calls)
	}
	if _, err := svc.GetGatewayRoute(ctx, route.ApplicationID, route.ID); err != nil {
		t.Fatal(err)
	}
}

func newRouteApplicationTest(t *testing.T) (*ApplicationRouteApplication, *service.ApplicationService, *domain.ApplicationGatewayRoute, *routeTestGateway, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&domain.Application{}, &domain.ApplicationEnvTarget{}, &domain.ApplicationGatewayRoute{}, &domain.ApplicationGatewayRouteBackend{}, &domain.ApplicationInstanceHealth{}); err != nil {
		t.Fatal(err)
	}
	svc := service.NewApplicationService(repository.NewApplicationRepository(db), repository.NewApplicationTargetRepository(db), repository.NewApplicationGatewayRouteRepository(db), repository.NewApplicationGatewayRouteBackendRepository(db), repository.NewMachineRepository(db), repository.NewApplicationInstanceHealthRepository(db))
	app := &domain.Application{Name: "route-test", RepoURL: "https://example.com/repo.git"}
	if err := db.Create(app).Error; err != nil {
		t.Fatal(err)
	}
	route := &domain.ApplicationGatewayRoute{ApplicationID: app.ID, Env: domain.EnvironmentTest, RouteKey: "old-test", Host: "47.109.43.173", PathPrefix: "/", Enabled: true}
	if err := db.Create(route).Error; err != nil {
		t.Fatal(err)
	}
	gateway := &routeTestGateway{}
	locks := &routeTestLocks{held: make(map[string]bool)}
	return NewApplicationRouteApplication(svc, gateway, locks), svc, route, gateway, db
}
