package application

import (
	"errors"
	"testing"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/service"
)

func TestRoutedPath(t *testing.T) {
	route := service.RouteSnapshot{PathPrefix: "/services/pay/prod", StripPrefix: true}
	got := RoutedPath("", route, "/services/pay/prod/v1/orders")
	if got != "/v1/orders" {
		t.Fatalf("RoutedPath = %q, want /v1/orders", got)
	}
}

func TestMatchLongestPath(t *testing.T) {
	app := NewGatewayApplication(nil, time.Second, WebSocketOptions{})
	app.snapshots = []service.RouteSnapshot{
		{
			RouteKey:   "pay",
			Host:       "api.example.com",
			PathPrefix: "/pay",
			Backends:   []service.BackendSnapshot{{TargetKey: "b", URL: "http://127.0.0.1:2"}},
		},
		{
			RouteKey:   "root",
			Host:       "api.example.com",
			PathPrefix: "/",
			Backends:   []service.BackendSnapshot{{TargetKey: "a", URL: "http://127.0.0.1:1"}},
		},
	}
	route, backend, ok, err := app.Match("api.example.com", "/pay/orders", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected match")
	}
	if route.RouteKey != "pay" || backend.TargetKey != "b" {
		t.Fatalf("matched %s/%s, want pay/b", route.RouteKey, backend.TargetKey)
	}
}

func TestMatchSpecificHostBeforeWildcard(t *testing.T) {
	app := NewGatewayApplication(nil, time.Second, WebSocketOptions{})
	app.snapshots = []service.RouteSnapshot{
		{
			RouteKey:   "specific",
			Host:       "api.example.com",
			PathPrefix: "/",
			Backends:   []service.BackendSnapshot{{TargetKey: "specific", URL: "http://127.0.0.1:1"}},
		},
		{
			RouteKey:   "wildcard",
			Host:       "*",
			PathPrefix: "/",
			Backends:   []service.BackendSnapshot{{TargetKey: "wildcard", URL: "http://127.0.0.1:2"}},
		},
	}
	route, backend, ok, err := app.Match("api.example.com", "/orders", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected match")
	}
	if route.RouteKey != "specific" || backend.TargetKey != "specific" {
		t.Fatalf("matched %s/%s, want specific/specific", route.RouteKey, backend.TargetKey)
	}
}

func TestMatchPinnedInstance(t *testing.T) {
	app := NewGatewayApplication(nil, time.Second, WebSocketOptions{})
	app.snapshots = []service.RouteSnapshot{
		{
			RouteKey:           "pay",
			Host:               "api.example.com",
			PathPrefix:         "/",
			InstanceSelectMode: domain.InstanceSelectModeEnabled,
			Backends: []service.BackendSnapshot{
				{TargetKey: "pay-a", InstanceName: "canary", URL: "http://127.0.0.1:1"},
				{TargetKey: "pay-b", InstanceName: "default", URL: "http://127.0.0.1:2"},
			},
		},
	}

	route, backend, ok, err := app.Match("api.example.com", "/orders", "canary")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok || backend.TargetKey != "pay-a" {
		t.Fatalf("expected pinned match on pay-a, got %+v ok=%v", backend, ok)
	}
	if route.RouteKey != "pay" {
		t.Fatalf("route mismatch: %s", route.RouteKey)
	}

	_, _, ok, err = app.Match("api.example.com", "/orders", "missing-instance")
	if ok || !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("expected ErrInstanceNotFound, got ok=%v err=%v", ok, err)
	}

	app.snapshots[0].InstanceSelectMode = domain.InstanceSelectModeDisabled
	_, _, ok, err = app.Match("api.example.com", "/orders", "canary")
	if ok || !errors.Is(err, ErrInstanceSelectDisabled) {
		t.Fatalf("expected ErrInstanceSelectDisabled, got ok=%v err=%v", ok, err)
	}
}
