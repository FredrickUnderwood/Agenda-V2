package pipeline

import (
	"context"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
)

// stubTargetLister feeds buildGatewayDrain a fixed sibling set.
type stubTargetLister struct {
	targets []*domain.ApplicationEnvTarget
}

func (s *stubTargetLister) ListTargetsByApplication(ctx context.Context, appID int64, env domain.Environment) ([]*domain.ApplicationEnvTarget, error) {
	return s.targets, nil
}

func drainTestBuilder(lister ApplicationTargetLister) *Builder {
	cfg := &config.Config{}
	cfg.Gateway.Enabled = true
	cfg.Gateway.BaseURL = "http://gw"
	cfg.Gateway.ServiceToken = "tok"
	return NewBuilder(cfg, nil, lister, nil, nil)
}

func drainApp() *domain.Application {
	return &domain.Application{
		ID:           1,
		Name:         "myapp",
		DeployMethod: domain.DeployMethodDocker,
		DeployConfig: `{"work_dir":".","compose_file":"docker-compose.yml"}`,
	}
}

// A single-backend route has nothing to fail over to, so decommissioning its
// only instance must DISABLE the route — while still carrying exactly one inert
// placeholder backend (the gateway rejects a zero-backend upsert) that never
// tries to register the dying instance's node.
func TestBuildGatewayDrain_SingleModeDisablesRoute(t *testing.T) {
	b := drainTestBuilder(nil)
	target := &domain.DeployTarget{
		App:    drainApp(),
		Branch: "master",
		EnvTarget: &domain.ApplicationEnvTarget{
			ID: 10, ApplicationID: 1, Env: domain.EnvironmentProd,
			InstanceName: "default", Port: 8080,
			GatewayRoutes: []*domain.ApplicationGatewayRoute{{
				RouteKey: "myapp-prod", Host: "myapp.example.com", PathPrefix: "/",
				Enabled: true, BackendMode: domain.GatewayBackendModeSingle,
			}},
		},
	}
	dockerCfg, _ := target.App.ParseDockerConfig()

	bp, err := b.buildGatewayDrain(context.Background(), target, dockerCfg, nil)
	if err != nil {
		t.Fatalf("buildGatewayDrain: %v", err)
	}
	step, ok := bp.Exec.(*GatewayRouteSyncStep)
	if !ok {
		t.Fatalf("expected GatewayRouteSyncStep, got %T", bp.Exec)
	}
	if len(step.Routes) != 1 {
		t.Fatalf("expected 1 route spec, got %d", len(step.Routes))
	}
	spec := step.Routes[0]
	if spec.Enabled {
		t.Errorf("single-mode route should be disabled on decommission")
	}
	if len(spec.Backends) != 1 {
		t.Fatalf("disabled route must keep exactly one placeholder backend, got %d", len(spec.Backends))
	}
	if spec.Backends[0].ProxyAgentBaseURL != "" {
		t.Errorf("placeholder backend must not carry proxy fields (would try to register the dying node): %+v", spec.Backends[0])
	}
	if spec.Backends[0].Healthy {
		t.Errorf("placeholder backend must be unhealthy")
	}
}

// An all_enabled route re-resolves over the surviving instances. The instance
// being decommissioned is marked stopped by the caller before drain, so it must
// be excluded; a healthy sibling keeps the route enabled and serving.
func TestBuildGatewayDrain_AllEnabledExcludesStoppedKeepsSurvivor(t *testing.T) {
	stopped := &domain.ApplicationEnvTarget{
		ID: 10, ApplicationID: 1, Env: domain.EnvironmentProd,
		InstanceName: "default", Port: 8080,
		Enabled: true, DesiredState: domain.RuntimeStateStopped,
	}
	survivor := &domain.ApplicationEnvTarget{
		ID: 11, ApplicationID: 1, Env: domain.EnvironmentProd,
		InstanceName: "blue", Port: 8081, Enabled: true,
	}
	b := drainTestBuilder(&stubTargetLister{targets: []*domain.ApplicationEnvTarget{stopped, survivor}})

	target := &domain.DeployTarget{
		App:    drainApp(),
		Branch: "master",
		EnvTarget: &domain.ApplicationEnvTarget{
			ID: 10, ApplicationID: 1, Env: domain.EnvironmentProd,
			InstanceName: "default", Port: 8080, DesiredState: domain.RuntimeStateStopped,
			GatewayRoutes: []*domain.ApplicationGatewayRoute{{
				RouteKey: "myapp-prod", Host: "myapp.example.com", PathPrefix: "/",
				Enabled: true, BackendMode: domain.GatewayBackendModeAllEnabled,
			}},
		},
	}
	dockerCfg, _ := target.App.ParseDockerConfig()

	bp, err := b.buildGatewayDrain(context.Background(), target, dockerCfg, nil)
	if err != nil {
		t.Fatalf("buildGatewayDrain: %v", err)
	}
	spec := bp.Exec.(*GatewayRouteSyncStep).Routes[0]
	if !spec.Enabled {
		t.Errorf("route with a surviving backend must stay enabled")
	}
	if len(spec.Backends) != 1 {
		t.Fatalf("expected exactly the surviving backend, got %d: %+v", len(spec.Backends), spec.Backends)
	}
	if spec.Backends[0].InstanceName != "blue" {
		t.Errorf("expected survivor 'blue' as the only backend, got %q", spec.Backends[0].InstanceName)
	}
}

// Gateway integration off → no drain step at all (Exec nil), so a self-hosted
// deploy without a gateway can still decommission (compose_down alone).
func TestBuildGatewayDrain_NoGatewayNoStep(t *testing.T) {
	cfg := &config.Config{} // Gateway.Enabled == false
	b := NewBuilder(cfg, nil, nil, nil, nil)
	target := &domain.DeployTarget{
		App:    drainApp(),
		Branch: "master",
		EnvTarget: &domain.ApplicationEnvTarget{
			ID: 10, ApplicationID: 1, Env: domain.EnvironmentProd, InstanceName: "default",
			GatewayRoutes: []*domain.ApplicationGatewayRoute{{RouteKey: "k", Host: "h", Enabled: true}},
		},
	}
	dockerCfg, _ := target.App.ParseDockerConfig()
	bp, err := b.buildGatewayDrain(context.Background(), target, dockerCfg, nil)
	if err != nil {
		t.Fatalf("buildGatewayDrain: %v", err)
	}
	if bp.Exec != nil {
		t.Errorf("expected no drain step when gateway is disabled")
	}
}
