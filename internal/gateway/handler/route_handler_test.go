package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/application"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/repository"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/service"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestRouteLifecycleRefreshesRuntimeAndChecksPermission(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&domain.Route{}, &domain.Backend{}, &domain.RouteHistory{}); err != nil {
		t.Fatal(err)
	}
	repo := repository.NewRouteRepository(db)
	routes := service.NewRouteService(repo, time.Second, time.Minute)
	gateway := application.NewGatewayApplication(routes, time.Second, application.WebSocketOptions{})
	srv := NewServer(&config.Config{ServiceTokens: []config.ServiceTokenConfig{{Name: "control", Token: "test-token", Perms: []string{"route.update"}}, {Name: "reader", Token: "read-token", Perms: []string{"route.read"}}}}, db, nil, routes, gateway, nil)
	ctx := context.Background()
	route := domain.Route{ApplicationID: 11, Env: "test", RouteKey: "old-test", ServiceName: "web", Host: "47.109.43.173", PathPrefix: "/", Status: domain.RouteStatusEnabled, CurrentReleaseID: "release-1"}
	backends := []domain.Backend{{TargetKey: "default", URL: "http://localhost:8080", Weight: 1, Enabled: true, Healthy: true}}
	if _, err := repo.UpsertRoute(ctx, route, backends, "test", "seed"); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := gateway.Match(route.Host, "/", ""); err != nil || !ok {
		t.Fatalf("initial route missing: %v", err)
	}
	call := func(method, suffix, token, body string, want int) {
		t.Helper()
		req := httptest.NewRequest(method, "/-/routes/old-test"+suffix, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(HeaderServiceToken, token)
		w := httptest.NewRecorder()
		srv.engine.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s, want %d", method, suffix, w.Code, w.Body.String(), want)
		}
	}
	body := `{"application_id":11,"env":"test"}`
	call(http.MethodDelete, "", "read-token", body, http.StatusForbidden)
	call(http.MethodDelete, "", "test-token", `{}`, http.StatusBadRequest)
	call(http.MethodDelete, "", "test-token", `{"application_id":12,"env":"test"}`, http.StatusBadRequest)
	call(http.MethodPost, "/disable", "test-token", body, http.StatusNoContent)
	if _, _, ok, err := gateway.Match(route.Host, "/", ""); err != nil || ok {
		t.Fatalf("disabled route still matched: %v", err)
	}
	if _, err := repo.GetRoute(ctx, route.RouteKey); err != nil {
		t.Fatalf("disable removed the row: %v", err)
	}
	// Reactivate to ensure delete refreshes an actively matching snapshot too.
	if _, err := repo.UpsertRoute(ctx, route, backends, "test", "reactivate"); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	call(http.MethodDelete, "", "test-token", body, http.StatusNoContent)
	if _, _, ok, err := gateway.Match(route.Host, "/", ""); err != nil || ok {
		t.Fatalf("deleted route still matched: %v", err)
	}
	call(http.MethodDelete, "", "test-token", body, http.StatusNoContent)
}
