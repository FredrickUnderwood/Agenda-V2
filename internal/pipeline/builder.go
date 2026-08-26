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
	"github.com/FredrickUnderwood/agenda-v2/internal/nodeproxy"
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
	fileChecker  EnvFileChecker
}

func NewBuilder(cfg *config.Config, machineSvc MachineGetter, targetLister ApplicationTargetLister, envConfigSvc EnvConfigGetter, fileChecker EnvFileChecker) *Builder {
	return &Builder{cfg: cfg, machineSvc: machineSvc, targetLister: targetLister, envConfigSvc: envConfigSvc, fileChecker: fileChecker}
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
	logDir, err := b.resolveInstanceLogDir(target, machine)
	if err != nil {
		return nil, "", err
	}
	filesDir, err := b.resolveEnvFilesDir(target, machine)
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
			AppName: target.App.Name, Branch: target.Branch, EnvName: string(target.Env()), InstanceName: targetInstanceName(target),
			LogDir:   logDir,
			FilesDir: filesDir,
			Env:      mergedEnv,

			FileChecker: b.fileChecker,
			AppID:       target.App.ID,
			MachineID:   targetMachineID(target),
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

// BuildTeardown returns the blueprint list for decommissioning one instance:
// drain its gateway traffic, then remove its containers. Unlike Build it clones
// nothing and brings nothing up. The returned localPath is present only to
// satisfy the Runner (no teardown step reads it).
//
// The caller must have already marked the target's DesiredState=stopped before
// calling this: the drain re-resolves all_enabled/selected routes over the
// surviving instances via the same ListTargetsByApplication path a deploy uses,
// and that exclusion relies on the stopped flag being visible there.
func (b *Builder) BuildTeardown(ctx context.Context, target *domain.DeployTarget) ([]Blueprint, string, error) {
	step, localPath, machine, dockerCfg, err := b.resolveContainerTeardown(ctx, target)
	if err != nil {
		return nil, "", err
	}

	bps := make([]Blueprint, 0, 3)
	drain, err := b.buildGatewayDrain(ctx, target, dockerCfg, machine)
	if err != nil {
		return nil, "", err
	}
	if drain.Exec != nil {
		bps = append(bps, drain)
		// Only meaningful after a route drain, and only for routes that can
		// actually hold a tunnel — so it is built from the same route set and
		// skipped entirely when none of them allows upgrades.
		if wsDrain := b.buildGatewayWSDrain(target); wsDrain.Exec != nil {
			bps = append(bps, wsDrain)
		}
	}
	bps = append(bps, step)
	return bps, localPath, nil
}

// buildGatewayWSDrain builds the wait-for-tunnels step that sits between the
// route drain and compose down. Returns an empty Blueprint when the gateway is
// off, the wait is disabled (gateway.ws_drain_timeout = 0), or no route on this
// instance has WebSocket enabled — in which case there is nothing that could
// still be attached and the extra step would only slow every teardown down.
func (b *Builder) buildGatewayWSDrain(target *domain.DeployTarget) Blueprint {
	if !b.cfg.Gateway.Enabled || b.cfg.Gateway.WSDrainTimeout.Duration <= 0 {
		return Blueprint{}
	}
	if target.EnvTarget == nil {
		return Blueprint{}
	}
	routeKeys := make([]string, 0, len(target.EnvTarget.GatewayRoutes))
	for _, route := range target.EnvTarget.GatewayRoutes {
		if route == nil || route.RouteKey == "" {
			continue
		}
		if normalizeUpgradeMode(route.UpgradeMode) != domain.GatewayUpgradeModeWebSocket {
			continue
		}
		routeKeys = append(routeKeys, route.RouteKey)
	}
	if len(routeKeys) == 0 {
		return Blueprint{}
	}
	return Blueprint{
		Name: "gateway_ws_drain",
		Type: domain.StepTypeGatewayWSDrain,
		Exec: &GatewayWSDrainStep{
			Client:       gatewayclient.NewClient(b.cfg.Gateway),
			InstanceName: targetInstanceName(target),
			RouteKeys:    routeKeys,
			Timeout:      b.cfg.Gateway.WSDrainTimeout.Duration,
		},
	}
}

// normalizeUpgradeMode treats anything unrecognized — including the empty
// string on rows written before the column existed — as "no upgrades", so a
// route only carries WebSockets when someone explicitly said so.
func normalizeUpgradeMode(mode domain.GatewayUpgradeMode) domain.GatewayUpgradeMode {
	if mode == domain.GatewayUpgradeModeWebSocket {
		return domain.GatewayUpgradeModeWebSocket
	}
	return domain.GatewayUpgradeModeNone
}

// BuildContainerTeardownStep returns just the compose_down step for an instance,
// without the gateway-drain step BuildTeardown prepends. It is the background
// reconcile path (finishing a teardown a machine was offline for): the gateway
// was already drained when the decommission was first requested — that call
// reaches the gateway, not the machine, so it succeeds even while the machine is
// down — so recovery only needs to re-attempt the container removal, and must
// not depend on gateway config being present.
func (b *Builder) BuildContainerTeardownStep(ctx context.Context, target *domain.DeployTarget) (Blueprint, string, error) {
	step, localPath, _, _, err := b.resolveContainerTeardown(ctx, target)
	if err != nil {
		return Blueprint{}, "", err
	}
	return step, localPath, nil
}

// resolveContainerTeardown resolves the machine, compose project name and an
// inert localPath for an instance and assembles its compose_down Blueprint.
// Shared by BuildTeardown (decommission) and BuildContainerTeardownStep
// (reconcile) so the two can never drift on how containers are identified.
func (b *Builder) resolveContainerTeardown(ctx context.Context, target *domain.DeployTarget) (Blueprint, string, *config.MachineConfig, *domain.DockerDeployConfig, error) {
	if target.App.DeployMethod != domain.DeployMethodDocker {
		return Blueprint{}, "", nil, nil, errors.New("only docker-method applications have containers to decommission")
	}
	dockerCfg, err := target.App.ParseDockerConfig()
	if err != nil {
		return Blueprint{}, "", nil, nil, err
	}
	machine, err := b.resolveDockerMachine(ctx, dockerCfg, target.EnvTarget)
	if err != nil {
		return Blueprint{}, "", nil, nil, err
	}

	instanceName := targetInstanceName(target)
	// projectName mirrors buildDocker's and needs the branch, which the caller
	// resolves from the instance's current running release. Empty branch → empty
	// projectName → ComposeDownStep relies on the agenda labels alone.
	projectName := ""
	if target.Branch != "" {
		projectName = util.Slug(target.App.Name) + "-" + util.Slug(target.Branch) + "-" + util.Slug(string(target.Env())) + "-" + util.Slug(instanceName)
	}

	// localPath is unused by teardown steps but Runner requires it non-empty.
	// Resolve best-effort; fall back to the workspace root when the branch is
	// unknown (ResolveLocalPath needs a branch).
	localPath := ""
	if target.Branch != "" {
		if lp, lpErr := b.resolveLocalPath(target, machine); lpErr == nil {
			localPath = lp
		}
	}
	if localPath == "" {
		localPath = b.workspaceRoot(machine)
	}

	step := Blueprint{
		Name: "compose_down", Type: domain.StepTypeComposeDown,
		Exec: &ComposeDownStep{
			Machine:      machine,
			AppName:      target.App.Name,
			EnvName:      string(target.Env()),
			InstanceName: instanceName,
			ProjectName:  projectName,
		},
	}
	return step, localPath, machine, dockerCfg, nil
}

// workspaceRoot returns the resolved workspace root for a machine (machine
// override, else global), used as an inert non-empty localPath for teardown.
func (b *Builder) workspaceRoot(machine *config.MachineConfig) string {
	if machine != nil && machine.WorkspaceRoot != "" {
		return machine.WorkspaceRoot
	}
	if b.cfg.WorkspaceRoot != "" {
		return b.cfg.WorkspaceRoot
	}
	return "/tmp"
}

// buildGatewayDrain builds the gateway-drain step for a decommission: for every
// app+env route, re-point it away from the instance being torn down. single-mode
// routes (the common one-instance case) have no other backend, so they are
// disabled; all_enabled/selected routes are re-resolved over the surviving
// instances (the caller has marked this instance stopped, so resolveRouteBackends
// skips it) and disabled only when no survivor remains.
//
// The gateway rejects an upsert with zero backends, so a route being disabled
// still carries a single self placeholder backend (with Enabled/Healthy false
// and no proxy fields, so the step never tries to register the dying instance's
// node). A disabled route is not served (LoadEnabledRoutes filters on status),
// so that placeholder never receives traffic.
//
// Returns an empty Blueprint (Exec nil) when gateway integration is off or the
// app has no routes.
func (b *Builder) buildGatewayDrain(ctx context.Context, target *domain.DeployTarget, dockerCfg *domain.DockerDeployConfig, machine *config.MachineConfig) (Blueprint, error) {
	if !b.cfg.Gateway.Enabled {
		return Blueprint{}, nil
	}
	routes := target.EnvTarget.GatewayRoutes
	if len(routes) == 0 {
		return Blueprint{}, nil
	}
	if b.cfg.Gateway.BaseURL == "" {
		return Blueprint{}, errors.New("gateway.base_url is required when gateway route is enabled")
	}
	if b.cfg.Gateway.ServiceToken == "" {
		return Blueprint{}, errors.New("gateway.service_token is required when gateway route is enabled")
	}
	scheme := b.cfg.Gateway.BackendScheme
	if scheme == "" {
		scheme = "http"
	}

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
		var backends []GatewayBackendSpec
		switch route.BackendMode {
		case "", domain.GatewayBackendModeSingle:
			// The decommissioning instance was this route's only backend; there is
			// nothing to fail over to, so disable it below.
			backends = nil
		default:
			bs, err := b.resolveRouteBackends(ctx, route, target.App.Name, dockerCfg, scheme, GatewayBackendSpec{}, loadSiblings)
			if err != nil {
				return Blueprint{}, err
			}
			backends = bs
		}
		enabled := len(backends) > 0
		if !enabled {
			// Keep a self placeholder so the upsert validates; the disabled status
			// keeps it out of the served set.
			backends = []GatewayBackendSpec{b.drainSelfPlaceholder(target, machine, scheme, route.BackendPath)}
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
			Enabled:            enabled,
			InstanceSelectMode: string(instanceSelectMode),
			InstanceHeader:     instanceHeader,
			Backends:           backends,

			// Carried through the drain too: the drain is an upsert of the same
			// route, so dropping these would silently disable WebSocket on a
			// route whose surviving instances are still serving tunnels.
			UpgradeMode:             string(normalizeUpgradeMode(route.UpgradeMode)),
			RequestTimeoutMs:        route.RequestTimeoutMs,
			WebsocketIdleTimeoutMs:  route.WebsocketIdleTimeoutMs,
			WebsocketMaxConnections: route.WebsocketMaxConnections,
			WebsocketAllowedOrigins: route.WebsocketAllowedOrigins,
		})
	}
	if len(specs) == 0 {
		return Blueprint{}, nil
	}
	return Blueprint{
		Name: "gateway_drain",
		Type: domain.StepTypeGatewayDrain,
		Exec: &GatewayRouteSyncStep{
			Client:        gatewayclient.NewClient(b.cfg.Gateway),
			ApplicationID: target.App.ID,
			ServiceName:   target.App.Name,
			Env:           string(target.Env()),
			Routes:        specs,
		},
	}, nil
}

// drainSelfPlaceholder builds an inert backend for the instance being torn down,
// used only to satisfy the gateway's "at least one backend" rule on a route that
// is being disabled. It deliberately carries no Proxy* fields so the sync step
// never attempts to register the dying instance's node, and a direct host:port
// URL (never a node proxy path) so it stays structurally valid without any live
// dependency.
func (b *Builder) drainSelfPlaceholder(target *domain.DeployTarget, machine *config.MachineConfig, scheme, backendPath string) GatewayBackendSpec {
	instanceName := targetInstanceName(target)
	port := 0
	if target.EnvTarget != nil {
		port = target.EnvTarget.Port
	}
	host := b.resolveBackendHost(machine)
	return GatewayBackendSpec{
		InstanceName: instanceName,
		TargetKey:    util.Slug(target.App.Name) + "-" + util.Slug(string(target.Env())) + "-" + util.Slug(instanceName) + "-" + strconv.Itoa(port),
		URL:          backendURL(scheme, host, port, backendPath),
		Weight:       1,
		Healthy:      false,
	}
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

// resolveInstanceLogDir picks the same workspace root as resolveLocalPath but
// derives the per-instance runtime log directory
// (<root>/run/<app>/<env>/<instance>/logs) instead of the code checkout. It is
// deliberately branch-independent so the deploy's bind-mount and the log
// reader resolve to the same directory regardless of which branch is running.
func (b *Builder) resolveInstanceLogDir(target *domain.DeployTarget, machine *config.MachineConfig) (string, error) {
	root := ""
	if machine != nil {
		root = machine.WorkspaceRoot
	}
	if root == "" {
		root = b.cfg.WorkspaceRoot
	}
	return git.InstanceLogDir(root, target.App.Name, string(target.Env()), targetInstanceName(target), machine.IsLocal())
}

// targetMachineID is the DB-managed machine an instance is bound to, or 0 when
// it is deployed to a config-file machine or to localhost.
func targetMachineID(target *domain.DeployTarget) int64 {
	if target == nil || target.EnvTarget == nil {
		return 0
	}
	return target.EnvTarget.MachineID
}

// resolveEnvFilesDir picks the same workspace root as resolveLocalPath but
// derives the environment's managed file directory
// (<root>/run/<app>/<env>/.files). Like the log dir it is branch-independent,
// and unlike it, it is shared by every instance of the environment.
func (b *Builder) resolveEnvFilesDir(target *domain.DeployTarget, machine *config.MachineConfig) (string, error) {
	root := ""
	if machine != nil {
		root = machine.WorkspaceRoot
	}
	if root == "" {
		root = b.cfg.WorkspaceRoot
	}
	return git.EnvFilesDir(root, target.App.Name, string(target.Env()), machine.IsLocal())
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

			UpgradeMode:             string(normalizeUpgradeMode(route.UpgradeMode)),
			RequestTimeoutMs:        route.RequestTimeoutMs,
			WebsocketIdleTimeoutMs:  route.WebsocketIdleTimeoutMs,
			WebsocketMaxConnections: route.WebsocketMaxConnections,
			WebsocketAllowedOrigins: route.WebsocketAllowedOrigins,
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
			if !sibling.Enabled || sibling.Stopped() {
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
			if !ok || !sibling.Enabled || sibling.Stopped() {
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
	// App-scoped proxy key: a bare instance name ("default") collides across
	// applications sharing a node (the registry is a flat map), silently
	// routing one app's host to another app's container. See nodeproxy.ProxyKey.
	proxyKey := nodeproxy.ProxyKey(appName, string(t.Env), instanceName)
	url, proxyBase, proxyToken, proxyPort, err := b.resolveBackend(machine, scheme, proxyKey, t.Port, backendPath)
	if err != nil {
		return GatewayBackendSpec{}, false
	}
	return GatewayBackendSpec{
		InstanceName:      instanceName,
		TargetKey:         targetKey,
		URL:               url,
		Weight:            weight,
		Healthy:           healthy,
		ProxyKey:          proxyKey,
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
// the URL points at the node's stable proxy path (proxyBaseURL + /i/<proxyKey>,
// the app-scoped key from nodeproxy.ProxyKey), hiding the drifting real port
// from the gateway; the real port is instead registered with the node under
// the same key. In ssh/local mode the URL is the direct host:port and the
// proxy fields are empty.
//
// An agent machine with an empty AgentProxyBaseURL is a misconfiguration and
// returns an error rather than silently falling back to host.docker.internal —
// that fallback is a control-plane-host concept and would route gateway traffic
// to the wrong machine (the same class of single-machine bug fixed in the
// health monitor). host.docker.internal is only ever valid for ssh/local.
func (b *Builder) resolveBackend(machine *config.MachineConfig, scheme, proxyKey string, port int, backendPath string) (url, proxyBaseURL, proxyToken string, proxyPort int, err error) {
	if machine != nil && machine.IsAgent() {
		if machine.AgentProxyBaseURL == "" {
			return "", "", "", 0, errors.New("agent-mode machine has no agent_proxy_base_url; cannot resolve gateway backend")
		}
		proxyURL := strings.TrimRight(machine.AgentProxyBaseURL, "/") + "/i/" + proxyKey + backendPath
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
