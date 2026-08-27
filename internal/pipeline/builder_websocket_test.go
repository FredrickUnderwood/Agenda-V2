package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	gwdomain "github.com/FredrickUnderwood/agenda-v2/internal/gateway/domain"
)

// TestUpgradeModeEnumsMatchAcrossPlanes guards a seam that the compiler cannot:
// the control plane's upgrade mode travels to the gateway as a bare JSON string
// and is re-typed on the other side. If either enum were renamed, routes would
// silently stop allowing WebSockets rather than fail to build.
func TestUpgradeModeEnumsMatchAcrossPlanes(t *testing.T) {
	if string(domain.GatewayUpgradeModeNone) != string(gwdomain.UpgradeModeNone) {
		t.Errorf("none: control plane %q vs gateway %q", domain.GatewayUpgradeModeNone, gwdomain.UpgradeModeNone)
	}
	if string(domain.GatewayUpgradeModeWebSocket) != string(gwdomain.UpgradeModeWebSocket) {
		t.Errorf("websocket: control plane %q vs gateway %q", domain.GatewayUpgradeModeWebSocket, gwdomain.UpgradeModeWebSocket)
	}
}

func wsTestBuilder(wsDrain time.Duration) *Builder {
	cfg := &config.Config{}
	cfg.Gateway.Enabled = true
	cfg.Gateway.BaseURL = "http://gw"
	cfg.Gateway.ServiceToken = "tok"
	cfg.Gateway.WSDrainTimeout.Duration = wsDrain
	return NewBuilder(cfg, nil, nil, nil, nil)
}

func wsTarget(route *domain.ApplicationGatewayRoute) *domain.DeployTarget {
	return &domain.DeployTarget{
		App:    drainApp(),
		Branch: "master",
		EnvTarget: &domain.ApplicationEnvTarget{
			ID: 10, ApplicationID: 1, Env: domain.EnvironmentProd,
			InstanceName: "default", Port: 8080,
			GatewayRoutes: []*domain.ApplicationGatewayRoute{route},
		},
	}
}

// The drain re-upserts the route, so every WebSocket setting has to travel with
// it. Dropping them would silently turn WebSocket off on a route whose
// surviving instances are still serving tunnels.
func TestBuildGatewayDrain_CarriesWebSocketSettings(t *testing.T) {
	b := wsTestBuilder(30 * time.Second)
	target := wsTarget(&domain.ApplicationGatewayRoute{
		RouteKey: "myapp-prod", Host: "myapp.example.com", PathPrefix: "/",
		Enabled: true, BackendMode: domain.GatewayBackendModeSingle,
		UpgradeMode:             domain.GatewayUpgradeModeWebSocket,
		RequestTimeoutMs:        15000,
		WebsocketIdleTimeoutMs:  60000,
		WebsocketMaxConnections: 500,
		WebsocketAllowedOrigins: "https://app.example.com",
	})
	dockerCfg, _ := target.App.ParseDockerConfig()

	bp, err := b.buildGatewayDrain(context.Background(), target, dockerCfg, nil)
	if err != nil {
		t.Fatalf("buildGatewayDrain: %v", err)
	}
	spec := bp.Exec.(*GatewayRouteSyncStep).Routes[0]
	if spec.UpgradeMode != string(domain.GatewayUpgradeModeWebSocket) {
		t.Errorf("UpgradeMode = %q, want websocket", spec.UpgradeMode)
	}
	if spec.RequestTimeoutMs != 15000 {
		t.Errorf("RequestTimeoutMs = %d, want 15000", spec.RequestTimeoutMs)
	}
	if spec.WebsocketIdleTimeoutMs != 60000 {
		t.Errorf("WebsocketIdleTimeoutMs = %d, want 60000", spec.WebsocketIdleTimeoutMs)
	}
	if spec.WebsocketMaxConnections != 500 {
		t.Errorf("WebsocketMaxConnections = %d, want 500", spec.WebsocketMaxConnections)
	}
	if spec.WebsocketAllowedOrigins != "https://app.example.com" {
		t.Errorf("WebsocketAllowedOrigins = %q", spec.WebsocketAllowedOrigins)
	}
}

// A route with an unrecognized or empty stored mode must be pushed as "none",
// never as an empty string the gateway would have to guess about.
func TestBuildGatewayDrain_NormalizesLegacyUpgradeMode(t *testing.T) {
	b := wsTestBuilder(30 * time.Second)
	target := wsTarget(&domain.ApplicationGatewayRoute{
		RouteKey: "myapp-prod", Host: "myapp.example.com", PathPrefix: "/",
		Enabled: true, BackendMode: domain.GatewayBackendModeSingle,
		UpgradeMode: "",
	})
	dockerCfg, _ := target.App.ParseDockerConfig()

	bp, err := b.buildGatewayDrain(context.Background(), target, dockerCfg, nil)
	if err != nil {
		t.Fatalf("buildGatewayDrain: %v", err)
	}
	if got := bp.Exec.(*GatewayRouteSyncStep).Routes[0].UpgradeMode; got != string(domain.GatewayUpgradeModeNone) {
		t.Errorf("UpgradeMode = %q, want none", got)
	}
}

func TestBuildGatewayWSDrain(t *testing.T) {
	wsRoute := &domain.ApplicationGatewayRoute{
		RouteKey: "myapp-prod", Host: "myapp.example.com", PathPrefix: "/",
		Enabled: true, BackendMode: domain.GatewayBackendModeSingle,
		UpgradeMode: domain.GatewayUpgradeModeWebSocket,
	}
	httpRoute := &domain.ApplicationGatewayRoute{
		RouteKey: "myapp-prod", Host: "myapp.example.com", PathPrefix: "/",
		Enabled: true, BackendMode: domain.GatewayBackendModeSingle,
	}

	t.Run("built for websocket routes", func(t *testing.T) {
		bp := wsTestBuilder(20 * time.Second).buildGatewayWSDrain(wsTarget(wsRoute))
		step, ok := bp.Exec.(*GatewayWSDrainStep)
		if !ok {
			t.Fatalf("expected GatewayWSDrainStep, got %T", bp.Exec)
		}
		if step.InstanceName != "default" {
			t.Errorf("InstanceName = %q", step.InstanceName)
		}
		if len(step.RouteKeys) != 1 || step.RouteKeys[0] != "myapp-prod" {
			t.Errorf("RouteKeys = %v", step.RouteKeys)
		}
		if step.Timeout != 20*time.Second {
			t.Errorf("Timeout = %v, want 20s", step.Timeout)
		}
	})

	// An HTTP-only app must not pay for a wait that can never find anything.
	t.Run("skipped without websocket routes", func(t *testing.T) {
		if bp := wsTestBuilder(20 * time.Second).buildGatewayWSDrain(wsTarget(httpRoute)); bp.Exec != nil {
			t.Errorf("built a ws drain step for an HTTP-only app: %+v", bp)
		}
	})

	t.Run("skipped when the wait is disabled", func(t *testing.T) {
		if bp := wsTestBuilder(0).buildGatewayWSDrain(wsTarget(wsRoute)); bp.Exec != nil {
			t.Errorf("built a ws drain step with ws_drain_timeout=0: %+v", bp)
		}
	})
}

// The teardown order is the whole point: stop new traffic, wait for the
// existing tunnels, and only then remove the containers underneath them.
func TestBuildTeardown_StepOrder(t *testing.T) {
	b := wsTestBuilder(20 * time.Second)
	target := wsTarget(&domain.ApplicationGatewayRoute{
		RouteKey: "myapp-prod", Host: "myapp.example.com", PathPrefix: "/",
		Enabled: true, BackendMode: domain.GatewayBackendModeSingle,
		UpgradeMode: domain.GatewayUpgradeModeWebSocket,
	})

	bps, _, err := b.BuildTeardown(context.Background(), target)
	if err != nil {
		t.Fatalf("BuildTeardown: %v", err)
	}
	got := make([]string, len(bps))
	for i, bp := range bps {
		got[i] = bp.Name
	}
	want := []string{"gateway_drain", "gateway_ws_drain", "compose_down"}
	if len(got) != len(want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("steps = %v, want %v", got, want)
		}
	}
}

func TestBuildTeardown_StepOrderWithoutWebSocket(t *testing.T) {
	b := wsTestBuilder(20 * time.Second)
	target := wsTarget(&domain.ApplicationGatewayRoute{
		RouteKey: "myapp-prod", Host: "myapp.example.com", PathPrefix: "/",
		Enabled: true, BackendMode: domain.GatewayBackendModeSingle,
	})

	bps, _, err := b.BuildTeardown(context.Background(), target)
	if err != nil {
		t.Fatalf("BuildTeardown: %v", err)
	}
	if len(bps) != 2 || bps[0].Name != "gateway_drain" || bps[1].Name != "compose_down" {
		t.Fatalf("steps = %+v, want [gateway_drain compose_down]", bps)
	}
}
