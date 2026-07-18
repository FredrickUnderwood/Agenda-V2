package pipeline

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/gatewayclient"
	"github.com/FredrickUnderwood/agenda-v2/internal/git"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
	"github.com/FredrickUnderwood/agenda-v2/internal/util"
)

// MachineGetter resolves a DB-managed machine by ID.
type MachineGetter interface {
	Get(ctx context.Context, id int64) (*domain.Machine, error)
}

// ApplicationTargetLister resolves every instance of an app+env (with health
// attached), used to resolve gateway backend_mode=all_enabled/selected into
// concrete backends at deploy-sync time.
type ApplicationTargetLister interface {
	ListTargetsByApplication(ctx context.Context, appID int64, env domain.Environment) ([]*domain.ApplicationEnvTarget, error)
}

// EnvConfigGetter resolves the env-level env var override layer (sits
// between the application-level baseline and the instance-level override).
type EnvConfigGetter interface {
	GetEnvVars(ctx context.Context, appID int64, env domain.Environment) (map[string]string, error)
}

// Builder turns a deploy target into an ordered list of step blueprints.
type Builder struct {
	cfg          *config.Config
	machineSvc   MachineGetter
	targetLister ApplicationTargetLister
	envConfigSvc EnvConfigGetter
}

func NewBuilder(cfg *config.Config, machineSvc MachineGetter, targetLister ApplicationTargetLister, envConfigSvc EnvConfigGetter) *Builder {
	return &Builder{cfg: cfg, machineSvc: machineSvc, targetLister: targetLister, envConfigSvc: envConfigSvc}
}

// Build returns the ordered blueprints plus the resolved on-machine clone
// directory (LocalPath) for the run. Callers pass LocalPath into Runner.Run
// so every step — including resumed runs that skip git_pull — sees the same
// path.
func (b *Builder) Build(ctx context.Context, target *domain.DeployTarget) ([]Blueprint, string, error) {
	switch target.App.DeployMethod {
	case domain.DeployMethodDocker:
		return b.buildDocker(ctx, target)
	case domain.DeployMethodAPI:
		return b.buildAPI(target)
	default:
		return nil, "", errors.New("unknown deploy method: " + string(target.App.DeployMethod))
	}
}

func (b *Builder) buildDocker(ctx context.Context, target *domain.DeployTarget) ([]Blueprint, string, error) {
	dockerCfg, err := target.App.ParseDockerConfig()
	if err != nil {
		return nil, "", err
	}
	machine, err := b.resolveDockerMachine(ctx, dockerCfg, target.EnvTarget)
	if err != nil {
		return nil, "", err
	}
	localPath, err := b.resolveLocalPath(target, machine)
	if err != nil {
		return nil, "", err
	}
	composeFile := dockerCfg.ComposeFile
	if composeFile == "" {
		composeFile = "docker-compose.yml"
	}
	projectName := util.Slug(target.App.Name) + "-" + util.Slug(target.Branch) + "-" + util.Slug(string(target.Env())) + "-" + util.Slug(targetInstanceName(target))
	port := 0
	metricsPort := 0
	if target.EnvTarget != nil {
		port = target.EnvTarget.Port
		if target.EnvTarget.MetricsEnabled {
			metricsPort = target.EnvTarget.MetricsPort
		}
	}

	mergedEnv, err := b.mergeEnv(ctx, target, dockerCfg)
	if err != nil {
		return nil, "", err
	}

	bps := []Blueprint{
		{Name: "git_pull", Type: domain.StepTypeGitPull, Exec: &GitPullStep{Machine: machine}},
	}
	if len(dockerCfg.PreCommands) > 0 {
		bps = append(bps, Blueprint{
			Name: "pre_commands", Type: domain.StepTypePreCommands,
			Exec: &ShellStep{Machine: machine, WorkDir: dockerCfg.WorkDir, Commands: dockerCfg.PreCommands},
		})
	}
	if dockerCfg.PullBeforeDeploy {
		bps = append(bps, Blueprint{
			Name: "compose_pull", Type: domain.StepTypeComposePull,
			Exec: &ComposePullStep{Machine: machine, WorkDir: dockerCfg.WorkDir, ComposeFile: composeFile, ProjectName: projectName, Port: port},
		})
	}
	bps = append(bps, Blueprint{
		Name: "compose_up", Type: domain.StepTypeComposeUp,
		Exec: &ComposeUpStep{
			Machine: machine, WorkDir: dockerCfg.WorkDir, ComposeFile: composeFile,
			ProjectName: projectName, Port: port, MetricsPort: metricsPort, Services: dockerCfg.Services,
			AppName: target.App.Name, Branch: target.Branch, InstanceName: targetInstanceName(target),
			Env: mergedEnv,
		},
	})
	if composeHealthCheckEnabled(dockerCfg) {
		bps = append(bps, Blueprint{
			Name: "compose_healthcheck", Type: domain.StepTypeComposeHealthCheck,
			Exec: &ComposeHealthCheckStep{
				Machine: machine, WorkDir: dockerCfg.WorkDir, ComposeFile: composeFile,
				ProjectName: projectName, Port: port, Services: dockerCfg.Services,
				Timeout:        composeHealthTimeoutFromConfig(dockerCfg.HealthCheck),
				Interval:       composeHealthIntervalFromConfig(dockerCfg.HealthCheck),
				RequireHealthy: composeHealthRequireHealthy(dockerCfg.HealthCheck),
			},
		})
	}
	if target.EnvTarget != nil {
		bp, err := b.buildGatewayRouteSync(ctx, target, dockerCfg, machine, port)
		if err != nil {
			return nil, "", err
		}
		if bp.Exec != nil {
			bps = append(bps, bp)
		}
	}
	return bps, localPath, nil
}

// mergeEnv layers application-level baseline (DockerDeployConfig.Env) <
// env-level (ApplicationEnvironment.EnvVars) < instance-level
// (ApplicationEnvTarget.EnvOverride), later layers winning on key conflict.
func (b *Builder) mergeEnv(ctx context.Context, target *domain.DeployTarget, dockerCfg *domain.DockerDeployConfig) (map[string]string, error) {
	merged := make(map[string]string, len(dockerCfg.Env))
	for k, v := range dockerCfg.Env {
		merged[k] = v
	}
	if b.envConfigSvc != nil {
		envVars, err := b.envConfigSvc.GetEnvVars(ctx, target.App.ID, target.Env())
		if err != nil {
			return nil, err
		}
		for k, v := range envVars {
			merged[k] = v
		}
	}
	if target.EnvTarget != nil {
		override, err := target.EnvTarget.ParseEnvOverride()
		if err != nil {
			return nil, err
		}
		for k, v := range override {
			merged[k] = v
		}
	}
	return merged, nil
}

func (b *Builder) buildAPI(target *domain.DeployTarget) ([]Blueprint, string, error) {
	apiCfg, err := target.App.ParseAPIConfig()
	if err != nil {
		return nil, "", err
	}
	// API deploys clone on the controller (machine == nil), so resolve against
	// the global workspace_root with ~ expansion enabled.
	localPath, err := b.resolveLocalPath(target, nil)
	if err != nil {
		return nil, "", err
	}
	return []Blueprint{
		{Name: "git_pull", Type: domain.StepTypeGitPull, Exec: &GitPullStep{Machine: nil}},
		{Name: "http_request", Type: domain.StepTypeAPIRequest, Exec: &HTTPRequestStep{Cfg: apiCfg}},
	}, localPath, nil
}

// resolveLocalPath picks the workspace root for the run: machine.WorkspaceRoot
// when set, otherwise the global Config.WorkspaceRoot. ~ is only expanded for
// local execution (machine.IsLocal()) — remote machines must use absolute paths.
func (b *Builder) resolveLocalPath(target *domain.DeployTarget, machine *config.MachineConfig) (string, error) {
	root := ""
	if machine != nil {
		root = machine.WorkspaceRoot
	}
	if root == "" {
		root = b.cfg.WorkspaceRoot
	}
	return git.ResolveLocalPath(target.App.RepoURL, target.Branch, root, machine.IsLocal())
}

// resolveDockerMachine: DB-managed machine (by ID, from the env target) →
// config machine (by name, from DeployConfig) → local.
func (b *Builder) resolveDockerMachine(ctx context.Context, dockerCfg *domain.DockerDeployConfig, envTarget *domain.ApplicationEnvTarget) (*config.MachineConfig, error) {
	if envTarget != nil && envTarget.MachineID > 0 {
		m, err := b.machineSvc.Get(ctx, envTarget.MachineID)
		if err != nil {
			return nil, err
		}
		return service.ToMachineConfig(m), nil
	}
	if dockerCfg.MachineID > 0 {
		m, err := b.machineSvc.Get(ctx, dockerCfg.MachineID)
		if err != nil {
			return nil, err
		}
		return service.ToMachineConfig(m), nil
	}
	return b.cfg.GetMachine(dockerCfg.Machine), nil
}

func (b *Builder) buildGatewayRouteSync(ctx context.Context, target *domain.DeployTarget, dockerCfg *domain.DockerDeployConfig, machine *config.MachineConfig, port int) (Blueprint, error) {
	envTarget := target.EnvTarget
	routes := envTarget.GatewayRoutes
	if len(routes) == 0 {
		return Blueprint{}, nil
	}
	if !b.cfg.Gateway.Enabled {
		return Blueprint{}, nil
	}
	if b.cfg.Gateway.BaseURL == "" {
		return Blueprint{}, errors.New("gateway.base_url is required when gateway route is enabled")
	}
	if b.cfg.Gateway.ServiceToken == "" {
		return Blueprint{}, errors.New("gateway.service_token is required when gateway route is enabled")
	}
	if port <= 0 {
		return Blueprint{}, errors.New("gateway route target port is required")
	}
	scheme := b.cfg.Gateway.BackendScheme
	if scheme == "" {
		scheme = "http"
	}
	selfTargetKey := util.Slug(target.App.Name) + "-" + util.Slug(string(target.Env())) + "-" + util.Slug(targetInstanceName(target)) + "-" + strconv.Itoa(port)
	selfInstanceName := targetInstanceName(target)

	// Only fetch sibling instances when a route actually needs them —
	// all_enabled/selected resolve backends beyond the instance currently
	// deploying; single (the default/common case) never does.
	var siblings []*domain.ApplicationEnvTarget
	siblingsLoaded := false
	loadSiblings := func() ([]*domain.ApplicationEnvTarget, error) {
		if siblingsLoaded {
			return siblings, nil
		}
		siblingsLoaded = true
		if b.targetLister == nil {
			return nil, nil
		}
		list, err := b.targetLister.ListTargetsByApplication(ctx, target.App.ID, target.Env())
		if err != nil {
			return nil, err
		}
		siblings = list
		return siblings, nil
	}

	specs := make([]GatewayRouteSpec, 0, len(routes))
	for _, route := range routes {
		if route == nil || route.RouteKey == "" {
			continue
		}
		pathPrefix := route.PathPrefix
		if pathPrefix == "" {
			pathPrefix = "/"
		}
		selfURL, selfProxyBase, selfProxyToken, selfProxyPort, err := b.resolveBackend(machine, scheme, selfInstanceName, port, route.BackendPath)
		if err != nil {
			return Blueprint{}, err
		}
		self := GatewayBackendSpec{
			InstanceName:      selfInstanceName,
			TargetKey:         selfTargetKey,
			URL:               selfURL,
			Weight:            1,
			Healthy:           route.Enabled,
			ProxyAgentBaseURL: selfProxyBase,
			ProxyAgentToken:   selfProxyToken,
			ProxyPort:         selfProxyPort,
		}
		backends, err := b.resolveRouteBackends(ctx, route, target.App.Name, dockerCfg, scheme, self, loadSiblings)
		if err != nil {
			return Blueprint{}, err
		}
		if len(backends) == 0 {
			continue
		}
		instanceSelectMode := route.InstanceSelectMode
		if instanceSelectMode == "" {
			instanceSelectMode = domain.GatewayInstanceSelectModeDisabled
		}
		instanceHeader := route.InstanceHeader
		if instanceHeader == "" {
			instanceHeader = domain.DefaultGatewayInstanceHeader
		}
		specs = append(specs, GatewayRouteSpec{
			RouteKey:           route.RouteKey,
			Host:               route.Host,
			PathPrefix:         pathPrefix,
			StripPrefix:        route.StripPrefix,
			Enabled:            route.Enabled,
			InstanceSelectMode: string(instanceSelectMode),
			InstanceHeader:     instanceHeader,
			Backends:           backends,
		})
	}
	if len(specs) == 0 {
		return Blueprint{}, nil
	}
	return Blueprint{
		Name: "gateway_routes_sync",
		Type: domain.StepTypeGatewayRouteSync,
		Exec: &GatewayRouteSyncStep{
			Client:        gatewayclient.NewClient(b.cfg.Gateway),
			ApplicationID: target.App.ID,
			ServiceName:   target.App.Name,
			Env:           string(target.Env()),
			Routes:        specs,
		},
	}, nil
}

// resolveRouteBackends turns a route's backend_mode into a concrete backend
// list: single is just the currently-deploying instance; all_enabled/selected
// resolve sibling instances' machine/port and derive Healthy from their
// health-check status (unmonitored = healthy, so unmonitored instances still
// receive traffic).
func (b *Builder) resolveRouteBackends(
	ctx context.Context,
	route *domain.ApplicationGatewayRoute,
	appName string,
	dockerCfg *domain.DockerDeployConfig,
	scheme string,
	self GatewayBackendSpec,
	loadSiblings func() ([]*domain.ApplicationEnvTarget, error),
) ([]GatewayBackendSpec, error) {
	switch route.BackendMode {
	case "", domain.GatewayBackendModeSingle:
		return []GatewayBackendSpec{self}, nil
	case domain.GatewayBackendModeAllEnabled:
		siblings, err := loadSiblings()
		if err != nil {
			return nil, err
		}
		backends := make([]GatewayBackendSpec, 0, len(siblings))
		for _, sibling := range siblings {
			if !sibling.Enabled {
				continue
			}
			backend, ok := b.backendSpecForTarget(ctx, sibling, appName, dockerCfg, scheme, route.BackendPath, 1)
			if !ok {
				continue
			}
			backends = append(backends, backend)
		}
		return backends, nil
	case domain.GatewayBackendModeSelected:
		siblings, err := loadSiblings()
		if err != nil {
			return nil, err
		}
		byID := make(map[int64]*domain.ApplicationEnvTarget, len(siblings))
		for _, sibling := range siblings {
			byID[sibling.ID] = sibling
		}
		backends := make([]GatewayBackendSpec, 0, len(route.Backends))
		for _, selected := range route.Backends {
			if selected == nil || !selected.Enabled {
				continue
			}
			sibling, ok := byID[selected.TargetID]
			if !ok || !sibling.Enabled {
				continue
			}
			weight := selected.Weight
			if weight <= 0 {
				weight = 1
			}
			backend, ok := b.backendSpecForTarget(ctx, sibling, appName, dockerCfg, scheme, route.BackendPath, weight)
			if !ok {
				continue
			}
			backends = append(backends, backend)
		}
		return backends, nil
	default:
		return nil, errors.New("unknown gateway backend_mode: " + string(route.BackendMode))
	}
}

// backendSpecForTarget resolves one sibling instance's machine/port into a
// backend spec. Resolution failures (deleted machine, unset port) are
// tolerated by skipping that single backend rather than failing the whole
// deploy — consistent with this codebase's no-FK, business-layer-tolerant
// approach to stale references.
func (b *Builder) backendSpecForTarget(ctx context.Context, t *domain.ApplicationEnvTarget, appName string, dockerCfg *domain.DockerDeployConfig, scheme, backendPath string, weight int) (GatewayBackendSpec, bool) {
	if t.Port <= 0 {
		return GatewayBackendSpec{}, false
	}
	machine, err := b.resolveDockerMachine(ctx, dockerCfg, t)
	if err != nil {
		return GatewayBackendSpec{}, false
	}
	instanceName := domain.NormalizeInstanceName(t.InstanceName)
	targetKey := util.Slug(appName) + "-" + util.Slug(string(t.Env)) + "-" + util.Slug(instanceName) + "-" + strconv.Itoa(t.Port)
	healthy := true
	if t.HealthCheckEnabled {
		healthy = t.Health != nil && t.Health.Status == domain.HealthStatusHealthy
	}
	url, proxyBase, proxyToken, proxyPort, err := b.resolveBackend(machine, scheme, instanceName, t.Port, backendPath)
	if err != nil {
		return GatewayBackendSpec{}, false
	}
	return GatewayBackendSpec{
		InstanceName:      instanceName,
		TargetKey:         targetKey,
		URL:               url,
		Weight:            weight,
		Healthy:           healthy,
		ProxyAgentBaseURL: proxyBase,
		ProxyAgentToken:   proxyToken,
		ProxyPort:         proxyPort,
	}, true
}

func (b *Builder) resolveBackendHost(machine *config.MachineConfig) string {
	if b.cfg.Gateway.BackendHost != "" {
		return b.cfg.Gateway.BackendHost
	}
	if machine != nil && machine.Host != "" {
		return machine.Host
	}
	return "host.docker.internal"
}

// resolveBackend returns the gateway-facing backend URL for an instance and, in
// agent mode, the proxy-registration fields the sync step needs. In agent mode
// the URL points at the node's stable proxy path (proxyBaseURL + /i/<instance>),
// hiding the drifting real port from the gateway; the real port is instead
// registered with the node. In ssh/local mode the URL is the direct host:port
// and the proxy fields are empty.
//
// An agent machine with an empty AgentProxyBaseURL is a misconfiguration and
// returns an error rather than silently falling back to host.docker.internal —
// that fallback is a control-plane-host concept and would route gateway traffic
// to the wrong machine (the same class of single-machine bug fixed in the
// health monitor). host.docker.internal is only ever valid for ssh/local.
func (b *Builder) resolveBackend(machine *config.MachineConfig, scheme, instanceName string, port int, backendPath string) (url, proxyBaseURL, proxyToken string, proxyPort int, err error) {
	if machine != nil && machine.IsAgent() {
		if machine.AgentProxyBaseURL == "" {
			return "", "", "", 0, errors.New("agent-mode machine has no agent_proxy_base_url; cannot resolve gateway backend")
		}
		proxyURL := strings.TrimRight(machine.AgentProxyBaseURL, "/") + "/i/" + instanceName + backendPath
		return proxyURL, machine.AgentBaseURL, machine.AgentToken, port, nil
	}
	host := b.resolveBackendHost(machine)
	return backendURL(scheme, host, port, backendPath), "", "", 0, nil
}

func backendURL(scheme, host string, port int, backendPath string) string {
	url := scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port))
	if backendPath != "" {
		url += backendPath
	}
	return url
}

func targetInstanceName(target *domain.DeployTarget) string {
	if target == nil || target.EnvTarget == nil {
		return domain.DefaultInstanceName
	}
	return domain.NormalizeInstanceName(target.EnvTarget.InstanceName)
}

// BlueprintToSteps turns blueprints into pipeline_step rows ready to insert
// for a fresh run. Caller sets DeployLogID.
func BlueprintToSteps(deployLogID int64, blueprints []Blueprint) []*domain.PipelineStep {
	out := make([]*domain.PipelineStep, len(blueprints))
	for i, bp := range blueprints {
		out[i] = &domain.PipelineStep{
			DeployLogID: deployLogID,
			Idx:         i,
			Name:        bp.Name,
			StepType:    bp.Type,
			Status:      domain.StepStatusPending,
			Attempt:     1,
		}
	}
	return out
}
