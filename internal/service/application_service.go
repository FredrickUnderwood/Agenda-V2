package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/git"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
)

type ApplicationService struct {
	apps          *repository.ApplicationRepository
	targets       *repository.ApplicationTargetRepository
	routes        *repository.ApplicationGatewayRouteRepository
	routeBackends *repository.ApplicationGatewayRouteBackendRepository
	machines      *repository.MachineRepository
	health        *repository.ApplicationInstanceHealthRepository
}

func NewApplicationService(
	apps *repository.ApplicationRepository,
	targets *repository.ApplicationTargetRepository,
	routes *repository.ApplicationGatewayRouteRepository,
	routeBackends *repository.ApplicationGatewayRouteBackendRepository,
	machines *repository.MachineRepository,
	health *repository.ApplicationInstanceHealthRepository,
) *ApplicationService {
	return &ApplicationService{apps: apps, targets: targets, routes: routes, routeBackends: routeBackends, machines: machines, health: health}
}

type ApplicationGatewayRouteRequest struct {
	ID                 int64                                   `json:"id"`
	RouteKey           string                                  `json:"route_key"`
	Host               string                                  `json:"host"`
	PathPrefix         string                                  `json:"path_prefix"`
	StripPrefix        bool                                    `json:"strip_prefix"`
	BackendPath        string                                  `json:"backend_path"`
	Enabled            *bool                                   `json:"enabled"`
	BackendMode        domain.GatewayBackendMode               `json:"backend_mode"`
	InstanceSelectMode domain.GatewayInstanceSelectMode        `json:"instance_select_mode"`
	InstanceHeader     string                                  `json:"instance_header"`
	Backends           []ApplicationGatewayRouteBackendRequest `json:"backends"`
	SortOrder          int                                     `json:"sort_order"`
}

type ApplicationGatewayRouteBackendRequest struct {
	TargetID int64 `json:"target_id"`
	Weight   int   `json:"weight"`
	Enabled  *bool `json:"enabled"`
}

type ApplicationEnvTargetRequest struct {
	Env                         domain.Environment                `json:"env"`
	InstanceName                string                            `json:"instance_name"`
	DisplayName                 string                            `json:"display_name"`
	MachineID                   int64                             `json:"machine_id"`
	Port                        int                               `json:"port"`
	Enabled                     *bool                             `json:"enabled"`
	HealthCheckEnabled          bool                              `json:"health_check_enabled"`
	HealthCheckType             string                            `json:"health_check_type"`
	HealthCheckScheme           string                            `json:"health_check_scheme"`
	HealthCheckHost             string                            `json:"health_check_host"`
	HealthCheckURL              string                            `json:"health_check_url"`
	HealthCheckPath             string                            `json:"health_check_path"`
	HealthCheckMethod           string                            `json:"health_check_method"`
	HealthCheckExpectedStatus   int                               `json:"health_check_expected_status"`
	HealthCheckTimeoutMS        int                               `json:"health_check_timeout_ms"`
	HealthCheckIntervalSec      int                               `json:"health_check_interval_sec"`
	HealthCheckFailureThreshold int                               `json:"health_check_failure_threshold"`
	HealthCheckSuccessThreshold int                               `json:"health_check_success_threshold"`
	MetricsEnabled              bool                              `json:"metrics_enabled"`
	MetricsPort                 int                               `json:"metrics_port"`
	EnvOverride                 map[string]string                 `json:"env_override,omitempty"`
	GatewayRoutes               *[]ApplicationGatewayRouteRequest `json:"gateway_routes,omitempty"`
}

type CreateApplicationRequest struct {
	Name         string                         `json:"name"          binding:"required"`
	RepoURL      string                         `json:"repo_url"      binding:"required"`
	DeployMethod domain.DeployMethod            `json:"deploy_method" binding:"required"`
	DeployConfig string                         `json:"deploy_config"`
	Description  string                         `json:"description"`
	Targets      *[]ApplicationEnvTargetRequest `json:"targets,omitempty"`
}

func (s *ApplicationService) Create(ctx context.Context, req CreateApplicationRequest) (*domain.Application, error) {
	if err := git.ValidateRepoURL(req.RepoURL); err != nil {
		return nil, err
	}
	if req.DeployConfig == "" {
		req.DeployConfig = "{}"
	}
	app := &domain.Application{
		Name:         req.Name,
		RepoURL:      req.RepoURL,
		DeployMethod: req.DeployMethod,
		DeployConfig: req.DeployConfig,
		Description:  req.Description,
	}
	if err := s.apps.Create(ctx, app); err != nil {
		return nil, err
	}
	if req.Targets != nil {
		if err := s.syncTargets(ctx, app.ID, *req.Targets); err != nil {
			_ = s.apps.Delete(ctx, app.ID)
			return nil, err
		}
	}
	logStruct("application created", app)
	return s.attachTargets(ctx, app)
}

func (s *ApplicationService) Get(ctx context.Context, id int64) (*domain.Application, error) {
	app, err := s.apps.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.attachTargets(ctx, app)
}

func (s *ApplicationService) List(ctx context.Context) ([]*domain.Application, error) {
	apps, err := s.apps.List(ctx)
	if err != nil {
		return nil, err
	}
	return s.attachTargetsToApps(ctx, apps)
}

type UpdateApplicationRequest struct {
	Name         string                         `json:"name"`
	RepoURL      string                         `json:"repo_url"`
	DeployMethod domain.DeployMethod            `json:"deploy_method"`
	DeployConfig string                         `json:"deploy_config"`
	Description  string                         `json:"description"`
	Targets      *[]ApplicationEnvTargetRequest `json:"targets,omitempty"`
}

func (s *ApplicationService) Update(ctx context.Context, id int64, req UpdateApplicationRequest) (*domain.Application, error) {
	app, err := s.apps.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Name != "" {
		app.Name = req.Name
	}
	if req.RepoURL != "" {
		if err := git.ValidateRepoURL(req.RepoURL); err != nil {
			return nil, err
		}
		app.RepoURL = req.RepoURL
	}
	if req.DeployMethod != "" {
		app.DeployMethod = req.DeployMethod
	}
	if req.DeployConfig != "" {
		app.DeployConfig = req.DeployConfig
	}
	if req.Description != "" {
		app.Description = req.Description
	}
	if err := s.apps.Update(ctx, app); err != nil {
		return nil, err
	}
	if req.Targets != nil {
		if err := s.syncTargets(ctx, app.ID, *req.Targets); err != nil {
			return nil, err
		}
	}
	logStruct("application updated", app)
	return s.attachTargets(ctx, app)
}

func (s *ApplicationService) Delete(ctx context.Context, id int64) error {
	if err := s.routes.DeleteByApplication(ctx, id); err != nil {
		return err
	}
	if err := s.health.DeleteByApplication(ctx, id); err != nil {
		return err
	}
	if err := s.targets.DeleteByApplication(ctx, id); err != nil {
		return err
	}
	if err := s.apps.Delete(ctx, id); err != nil {
		return err
	}
	logger.L().Info("application deleted", zap.Int64("id", id))
	return nil
}

func (s *ApplicationService) GetTargetForEnv(ctx context.Context, appID int64, env domain.Environment) (*domain.ApplicationEnvTarget, error) {
	return s.GetTargetForEnvInstance(ctx, appID, env, domain.DefaultInstanceName)
}

func (s *ApplicationService) GetTargetForEnvInstance(ctx context.Context, appID int64, env domain.Environment, instanceName string) (*domain.ApplicationEnvTarget, error) {
	env = domain.DefaultEnvironment(env)
	if !env.Valid() {
		return nil, errors.New(fmt.Sprintf("invalid env %q", env))
	}
	instanceName = domain.NormalizeInstanceName(instanceName)
	target, err := s.targets.GetByApplicationEnvInstance(ctx, appID, env, instanceName)
	if err != nil {
		return nil, err
	}
	if !target.Enabled {
		return nil, errors.New(fmt.Sprintf("application %d does not deploy to %s/%s", appID, env, instanceName))
	}
	routes, err := s.routes.ListByApplicationEnv(ctx, appID, env)
	if err != nil {
		return nil, err
	}
	// Load each route's selected-backend list (route.Backends is gorm:"-", not
	// auto-fetched). The deploy pipeline reads this target's GatewayRoutes and,
	// for backend_mode=selected, resolves backends from route.Backends — without
	// this attach that list is empty, the route resolves to zero backends, and
	// gateway_routes_sync is silently skipped, so selected mode never reaches the
	// gateway. The display path (attachGatewayRoutesToTargets) already does this.
	if err := s.attachRouteBackends(ctx, routes); err != nil {
		return nil, err
	}
	target.GatewayRoutes = routes
	return target, nil
}

// GetTargetByID resolves one instance by its target ID and asserts it belongs
// to appID (the URL path carries both, so a mismatch is a client error, not a
// silent cross-application read). Unlike GetTargetForEnvInstance it does not
// require Enabled — the lifecycle path must be able to load an instance in any
// state to act on it.
func (s *ApplicationService) GetTargetByID(ctx context.Context, appID, targetID int64) (*domain.ApplicationEnvTarget, error) {
	target, err := s.targets.GetByID(ctx, targetID)
	if err != nil {
		return nil, err
	}
	if target.ApplicationID != appID {
		return nil, errors.New(fmt.Sprintf("application %d has no instance %d", appID, targetID))
	}
	return target, nil
}

// SetInstanceDesiredState records the operator's runtime intent for an instance
// (running/stopped). It owns the desired_state column exclusively; see
// ApplicationTargetRepository.UpdateDesiredState.
func (s *ApplicationService) SetInstanceDesiredState(ctx context.Context, targetID int64, state domain.RuntimeState) error {
	return s.targets.UpdateDesiredState(ctx, targetID, state)
}

func (s *ApplicationService) ListTargetsByApplication(ctx context.Context, appID int64, env domain.Environment) ([]*domain.ApplicationEnvTarget, error) {
	app, err := s.apps.GetByID(ctx, appID)
	if err != nil {
		return nil, err
	}
	targets, err := s.targets.ListByApplication(ctx, appID)
	if err != nil {
		return nil, err
	}
	filtered := make([]*domain.ApplicationEnvTarget, 0, len(targets))
	for _, target := range targets {
		if env != "" && target.Env != env {
			continue
		}
		filtered = append(filtered, target)
	}
	if err := s.attachHealthToTargets(ctx, filtered); err != nil {
		return nil, err
	}
	if err := s.attachGatewayRoutesToTargets(ctx, app, filtered); err != nil {
		return nil, err
	}
	return filtered, nil
}

func (s *ApplicationService) syncTargets(ctx context.Context, appID int64, reqs []ApplicationEnvTargetRequest) error {
	seen := make(map[string]struct{}, len(reqs))
	seenEnv := make(map[domain.Environment]struct{}, len(reqs))
	seenMachinePort := make(map[string]struct{}, len(reqs))
	type targetSyncItem struct {
		target *domain.ApplicationEnvTarget
	}
	items := make([]targetSyncItem, 0, len(reqs))
	routesByEnv := make(map[domain.Environment][]*domain.ApplicationGatewayRoute, len(reqs))
	// routesProvidedEnv tracks which envs had at least one target request that
	// explicitly set gateway_routes (nil vs. non-nil, not nil vs. empty — a
	// target that omits the field entirely means "don't touch this env's
	// routes", same as a multi-instance env's non-representative targets are
	// expected to send an explicit `[]`). Without this, saving an unrelated
	// field on any target (e.g. from the Instances tab) would sync an empty
	// route list for that env and silently disable every route in it.
	routesProvidedEnv := make(map[domain.Environment]bool, len(reqs))
	app, err := s.apps.GetByID(ctx, appID)
	if err != nil {
		return err
	}
	existing, err := s.targets.ListByApplication(ctx, appID)
	if err != nil {
		return err
	}
	existingByKey := make(map[string]*domain.ApplicationEnvTarget, len(existing))
	for _, t := range existing {
		t.InstanceName = domain.NormalizeInstanceName(t.InstanceName)
		existingByKey[targetIdentityKey(t.Env, t.InstanceName)] = t
	}

	for _, req := range reqs {
		env := domain.DefaultEnvironment(req.Env)
		if !env.Valid() {
			return errors.New(fmt.Sprintf("invalid env %q", env))
		}
		instanceName := domain.NormalizeInstanceName(req.InstanceName)
		if !domain.ValidInstanceName(instanceName) {
			return errors.New(fmt.Sprintf("invalid %s target instance name %q", env, instanceName))
		}
		key := targetIdentityKey(env, instanceName)
		if _, ok := seen[key]; ok {
			return errors.New(fmt.Sprintf("duplicate deploy target %s/%s", env, instanceName))
		}
		seen[key] = struct{}{}
		seenEnv[env] = struct{}{}

		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		envOverrideJSON, err := marshalEnvOverride(req.EnvOverride)
		if err != nil {
			return err
		}
		target := &domain.ApplicationEnvTarget{
			ApplicationID:               appID,
			Env:                         env,
			InstanceName:                instanceName,
			DisplayName:                 strings.TrimSpace(req.DisplayName),
			MachineID:                   req.MachineID,
			Port:                        req.Port,
			Enabled:                     enabled,
			HealthCheckEnabled:          req.HealthCheckEnabled,
			HealthCheckType:             strings.TrimSpace(req.HealthCheckType),
			HealthCheckScheme:           strings.TrimSpace(req.HealthCheckScheme),
			HealthCheckHost:             strings.TrimSpace(req.HealthCheckHost),
			HealthCheckURL:              strings.TrimSpace(req.HealthCheckURL),
			HealthCheckPath:             strings.TrimSpace(req.HealthCheckPath),
			HealthCheckMethod:           strings.TrimSpace(req.HealthCheckMethod),
			HealthCheckExpectedStatus:   req.HealthCheckExpectedStatus,
			HealthCheckTimeoutMS:        req.HealthCheckTimeoutMS,
			HealthCheckIntervalSec:      req.HealthCheckIntervalSec,
			HealthCheckFailureThreshold: req.HealthCheckFailureThreshold,
			HealthCheckSuccessThreshold: req.HealthCheckSuccessThreshold,
			MetricsEnabled:              req.MetricsEnabled,
			MetricsPort:                 req.MetricsPort,
			EnvOverrideJSON:             envOverrideJSON,
		}
		normalizeHealthCheckConfig(target)
		if old := existingByKey[key]; old != nil {
			target.ID = old.ID
		}

		routes, err := s.gatewayRoutesFromRequest(appID, app.Name, env, enabled, req)
		if err != nil {
			return err
		}
		if req.GatewayRoutes != nil {
			routesProvidedEnv[env] = true
		}

		if target.Enabled && target.MachineID > 0 {
			mpKey := targetMachinePortKey(target.MachineID, target.Port)
			if _, ok := seenMachinePort[mpKey]; ok {
				return errors.New(fmt.Sprintf("duplicate enabled target port %d on machine %d", target.Port, target.MachineID))
			}
			seenMachinePort[mpKey] = struct{}{}
		}
		if err := s.validateTarget(ctx, target); err != nil {
			return err
		}
		if err := s.validateGatewayRoutes(ctx, target, routes); err != nil {
			return err
		}
		items = append(items, targetSyncItem{target: target})
		routesByEnv[env] = append(routesByEnv[env], routes...)
	}

	if err := validateAccumulatedGatewayRoutes(routesByEnv); err != nil {
		return err
	}

	for _, item := range items {
		if err := s.targets.Upsert(ctx, item.target); err != nil {
			return err
		}
	}

	targetIDsByEnv := make(map[domain.Environment]map[int64]struct{}, len(items))
	for _, item := range items {
		set := targetIDsByEnv[item.target.Env]
		if set == nil {
			set = make(map[int64]struct{})
			targetIDsByEnv[item.target.Env] = set
		}
		set[item.target.ID] = struct{}{}
	}

	for env, routes := range routesByEnv {
		if !routesProvidedEnv[env] {
			// No target in this request touched this env's routes at all —
			// leave whatever is already stored (and live on the gateway)
			// alone instead of syncing an empty list and disabling it.
			continue
		}
		if err := s.routes.SyncByApplicationEnv(ctx, appID, env, routes); err != nil {
			return err
		}
		validIDs := targetIDsByEnv[env]
		for _, route := range routes {
			if route.BackendMode != domain.GatewayBackendModeSelected {
				if err := s.routeBackends.SyncByRoute(ctx, route.ID, nil); err != nil {
					return err
				}
				continue
			}
			for _, backend := range route.Backends {
				if _, ok := validIDs[backend.TargetID]; !ok {
					return errors.New(fmt.Sprintf("gateway route %q backend target %d is not a %s instance of this application",
						route.RouteKey, backend.TargetID, env))
				}
			}
			if err := s.routeBackends.SyncByRoute(ctx, route.ID, route.Backends); err != nil {
				return err
			}
		}
	}
	for _, old := range existing {
		old.InstanceName = domain.NormalizeInstanceName(old.InstanceName)
		if _, ok := seen[targetIdentityKey(old.Env, old.InstanceName)]; ok {
			continue
		}
		if _, envStillPresent := seenEnv[old.Env]; !envStillPresent {
			if err := s.routes.SyncByApplicationEnv(ctx, appID, old.Env, nil); err != nil {
				return err
			}
		}
		if err := s.targets.DeleteByApplicationEnvInstance(ctx, appID, old.Env, old.InstanceName); err != nil {
			return err
		}
	}
	return nil
}

func targetIdentityKey(env domain.Environment, instanceName string) string {
	return fmt.Sprintf("%s:%s", env, domain.NormalizeInstanceName(instanceName))
}

func targetMachinePortKey(machineID int64, port int) string {
	return fmt.Sprintf("%d:%d", machineID, port)
}

func marshalEnvOverride(m map[string]string) (string, error) {
	if len(m) == 0 {
		return "", nil
	}
	data, err := sonicMarshal(m)
	if err != nil {
		return "", err
	}
	return data, nil
}

func normalizeGatewayPathPrefix(pathPrefix string) string {
	pathPrefix = strings.TrimSpace(pathPrefix)
	if pathPrefix == "" {
		return "/"
	}
	if !strings.HasPrefix(pathPrefix, "/") {
		pathPrefix = "/" + pathPrefix
	}
	if len(pathPrefix) > 1 {
		pathPrefix = strings.TrimRight(pathPrefix, "/")
		if pathPrefix == "" {
			return "/"
		}
	}
	return pathPrefix
}

func normalizeGatewayBackendPath(pathPrefix string) string {
	pathPrefix = strings.TrimSpace(pathPrefix)
	if pathPrefix == "" || pathPrefix == "/" {
		return ""
	}
	if !strings.HasPrefix(pathPrefix, "/") {
		pathPrefix = "/" + pathPrefix
	}
	return strings.TrimRight(pathPrefix, "/")
}

func normalizeHealthCheckConfig(target *domain.ApplicationEnvTarget) {
	if target.InstanceName == "" {
		target.InstanceName = domain.DefaultInstanceName
	}
	if target.HealthCheckType == "" {
		target.HealthCheckType = "http"
	}
	target.HealthCheckType = strings.ToLower(target.HealthCheckType)
	if target.HealthCheckScheme == "" {
		target.HealthCheckScheme = "http"
	}
	target.HealthCheckScheme = strings.ToLower(target.HealthCheckScheme)
	if target.HealthCheckPath == "" {
		target.HealthCheckPath = "/healthz"
	}
	if !strings.HasPrefix(target.HealthCheckPath, "/") {
		target.HealthCheckPath = "/" + target.HealthCheckPath
	}
	if target.HealthCheckMethod == "" {
		target.HealthCheckMethod = "GET"
	}
	target.HealthCheckMethod = strings.ToUpper(target.HealthCheckMethod)
	if target.HealthCheckExpectedStatus == 0 {
		target.HealthCheckExpectedStatus = 200
	}
	if target.HealthCheckTimeoutMS == 0 {
		target.HealthCheckTimeoutMS = 3000
	}
	if target.HealthCheckIntervalSec == 0 {
		target.HealthCheckIntervalSec = 30
	}
	if target.HealthCheckFailureThreshold == 0 {
		target.HealthCheckFailureThreshold = 3
	}
	if target.HealthCheckSuccessThreshold == 0 {
		target.HealthCheckSuccessThreshold = 1
	}
}

func (s *ApplicationService) gatewayRoutesFromRequest(appID int64, appName string, env domain.Environment, targetEnabled bool, req ApplicationEnvTargetRequest) ([]*domain.ApplicationGatewayRoute, error) {
	if req.GatewayRoutes == nil {
		return nil, nil
	}
	routes := make([]*domain.ApplicationGatewayRoute, 0, len(*req.GatewayRoutes))
	for i, routeReq := range *req.GatewayRoutes {
		enabled := true
		if routeReq.Enabled != nil {
			enabled = *routeReq.Enabled
		}
		if !targetEnabled {
			enabled = false
		}
		backendMode, err := normalizeGatewayBackendMode(routeReq.BackendMode)
		if err != nil {
			return nil, err
		}
		instanceSelectMode, err := normalizeGatewayInstanceSelectMode(routeReq.InstanceSelectMode)
		if err != nil {
			return nil, err
		}
		instanceHeader := strings.TrimSpace(routeReq.InstanceHeader)
		if instanceHeader == "" {
			instanceHeader = domain.DefaultGatewayInstanceHeader
		}
		route := &domain.ApplicationGatewayRoute{
			ID:                 routeReq.ID,
			ApplicationID:      appID,
			Env:                env,
			RouteKey:           strings.TrimSpace(routeReq.RouteKey),
			Host:               strings.TrimSpace(routeReq.Host),
			PathPrefix:         normalizeGatewayPathPrefix(routeReq.PathPrefix),
			StripPrefix:        routeReq.StripPrefix,
			BackendPath:        normalizeGatewayBackendPath(routeReq.BackendPath),
			Enabled:            enabled,
			BackendMode:        backendMode,
			InstanceSelectMode: instanceSelectMode,
			InstanceHeader:     instanceHeader,
			SortOrder:          routeReq.SortOrder,
		}
		if route.SortOrder == 0 {
			route.SortOrder = i
		}
		if backendMode == domain.GatewayBackendModeSelected {
			route.Backends = make([]*domain.ApplicationGatewayRouteBackend, 0, len(routeReq.Backends))
			for _, b := range routeReq.Backends {
				backendEnabled := true
				if b.Enabled != nil {
					backendEnabled = *b.Enabled
				}
				weight := b.Weight
				if weight <= 0 {
					weight = 1
				}
				route.Backends = append(route.Backends, &domain.ApplicationGatewayRouteBackend{
					TargetID: b.TargetID,
					Weight:   weight,
					Enabled:  backendEnabled,
				})
			}
		}
		if isEmptyDisabledGatewayRoute(route) {
			continue
		}
		routes = append(routes, route)
	}
	return routes, nil
}

func normalizeGatewayBackendMode(mode domain.GatewayBackendMode) (domain.GatewayBackendMode, error) {
	switch mode {
	case "":
		return domain.GatewayBackendModeSingle, nil
	case domain.GatewayBackendModeSingle, domain.GatewayBackendModeAllEnabled, domain.GatewayBackendModeSelected:
		return mode, nil
	default:
		return "", errors.New(fmt.Sprintf("invalid gateway backend_mode %q", mode))
	}
}

func normalizeGatewayInstanceSelectMode(mode domain.GatewayInstanceSelectMode) (domain.GatewayInstanceSelectMode, error) {
	switch mode {
	case "":
		return domain.GatewayInstanceSelectModeDisabled, nil
	case domain.GatewayInstanceSelectModeDisabled, domain.GatewayInstanceSelectModeEnabled:
		return mode, nil
	default:
		return "", errors.New(fmt.Sprintf("invalid gateway instance_select_mode %q", mode))
	}
}

func isEmptyDisabledGatewayRoute(route *domain.ApplicationGatewayRoute) bool {
	if route == nil || route.Enabled {
		return false
	}
	return route.RouteKey == "" &&
		route.Host == "" &&
		(route.PathPrefix == "" || route.PathPrefix == "/") &&
		route.BackendPath == ""
}

func (s *ApplicationService) validateTarget(ctx context.Context, target *domain.ApplicationEnvTarget) error {
	if target.InstanceName == "" {
		target.InstanceName = domain.DefaultInstanceName
	}
	if !domain.ValidInstanceName(target.InstanceName) {
		return errors.New(fmt.Sprintf("%s target instance name %q is invalid", target.Env, target.InstanceName))
	}
	if target.HealthCheckEnabled && !target.Enabled {
		return errors.New(fmt.Sprintf("%s/%s health check cannot be enabled on a disabled target", target.Env, target.InstanceName))
	}
	if err := validateHealthCheckConfig(target); err != nil {
		return err
	}
	if !target.Enabled {
		return nil
	}
	if target.Port <= 0 || target.Port > 65535 {
		return errors.New(fmt.Sprintf("%s target port must be in (0, 65535]", target.Env))
	}
	if target.MachineID <= 0 {
		return nil
	}
	machine, err := s.machines.GetByID(ctx, target.MachineID)
	if err != nil {
		return err
	}
	if machine.MachineType != "" && machine.MachineType != target.Env {
		return errors.New(fmt.Sprintf("%s target must use a %s machine, got %s", target.Env, target.Env, machine.MachineType))
	}
	conflicts, err := s.targets.ListEnabledByMachinePort(ctx, target.MachineID, target.Port)
	if err != nil {
		return err
	}
	for _, other := range conflicts {
		if other.ID == target.ID {
			continue
		}
		return errors.New(fmt.Sprintf("port %d already used by application %d %s target on machine %d",
			target.Port, other.ApplicationID, other.Env, target.MachineID))
	}
	return nil
}

func validateHealthCheckConfig(target *domain.ApplicationEnvTarget) error {
	if !target.HealthCheckEnabled {
		return nil
	}
	if target.HealthCheckType != "http" {
		return errors.New(fmt.Sprintf("%s/%s health check type %q is not supported", target.Env, target.InstanceName, target.HealthCheckType))
	}
	if target.HealthCheckScheme != "http" && target.HealthCheckScheme != "https" {
		return errors.New(fmt.Sprintf("%s/%s health check scheme must be http or https", target.Env, target.InstanceName))
	}
	switch target.HealthCheckMethod {
	case "GET", "HEAD":
	default:
		return errors.New(fmt.Sprintf("%s/%s health check method must be GET or HEAD", target.Env, target.InstanceName))
	}
	if strings.TrimSpace(target.HealthCheckURL) == "" && strings.TrimSpace(target.HealthCheckPath) == "" {
		return errors.New(fmt.Sprintf("%s/%s health check path is required", target.Env, target.InstanceName))
	}
	if target.HealthCheckExpectedStatus < 100 || target.HealthCheckExpectedStatus > 599 {
		return errors.New(fmt.Sprintf("%s/%s health check expected status must be in [100, 599]", target.Env, target.InstanceName))
	}
	if target.HealthCheckTimeoutMS < 100 || target.HealthCheckTimeoutMS > 60000 {
		return errors.New(fmt.Sprintf("%s/%s health check timeout must be in [100, 60000] ms", target.Env, target.InstanceName))
	}
	if target.HealthCheckIntervalSec < 5 || target.HealthCheckIntervalSec > 3600 {
		return errors.New(fmt.Sprintf("%s/%s health check interval must be in [5, 3600] seconds", target.Env, target.InstanceName))
	}
	if target.HealthCheckFailureThreshold < 1 || target.HealthCheckFailureThreshold > 10 {
		return errors.New(fmt.Sprintf("%s/%s health check failure threshold must be in [1, 10]", target.Env, target.InstanceName))
	}
	if target.HealthCheckSuccessThreshold < 1 || target.HealthCheckSuccessThreshold > 10 {
		return errors.New(fmt.Sprintf("%s/%s health check success threshold must be in [1, 10]", target.Env, target.InstanceName))
	}
	return nil
}

func (s *ApplicationService) validateGatewayRoutes(ctx context.Context, target *domain.ApplicationEnvTarget, routes []*domain.ApplicationGatewayRoute) error {
	seenKeys := make(map[string]struct{}, len(routes))
	seenHostPaths := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if route.RouteKey == "" {
			return errors.New(fmt.Sprintf("%s target gateway route key is required", target.Env))
		}
		if _, ok := seenKeys[route.RouteKey]; ok {
			return errors.New(fmt.Sprintf("duplicate gateway route key %q", route.RouteKey))
		}
		seenKeys[route.RouteKey] = struct{}{}

		existingKey, err := s.routes.FindByRouteKey(ctx, route.RouteKey)
		if err != nil {
			return err
		}
		if existingKey != nil && !sameGatewayRoute(existingKey, route) {
			return errors.New(fmt.Sprintf("gateway route key %q already used by application %d %s target",
				route.RouteKey, existingKey.ApplicationID, existingKey.Env))
		}

		if route.Enabled && route.Host == "" {
			return errors.New(fmt.Sprintf("%s target gateway route %q host is required", target.Env, route.RouteKey))
		}
		if route.BackendMode == domain.GatewayBackendModeSelected && len(route.Backends) == 0 {
			return errors.New(fmt.Sprintf("%s target gateway route %q backend_mode=selected requires at least one backend", target.Env, route.RouteKey))
		}
		if route.Host == "" {
			continue
		}
		hostPath := route.Host + "\x00" + route.PathPrefix
		if _, ok := seenHostPaths[hostPath]; ok {
			return errors.New(fmt.Sprintf("duplicate gateway host/path %s%s", route.Host, route.PathPrefix))
		}
		seenHostPaths[hostPath] = struct{}{}

		existingHostPath, err := s.routes.FindByHostPath(ctx, route.Host, route.PathPrefix)
		if err != nil {
			return err
		}
		if existingHostPath != nil && !sameGatewayRoute(existingHostPath, route) {
			return errors.New(fmt.Sprintf("gateway host/path %s%s already used by application %d %s target",
				route.Host, route.PathPrefix, existingHostPath.ApplicationID, existingHostPath.Env))
		}
	}
	return nil
}

func sameGatewayRoute(existing, route *domain.ApplicationGatewayRoute) bool {
	if existing == nil || route == nil {
		return false
	}
	if route.ID > 0 && existing.ID == route.ID {
		return true
	}
	return existing.ApplicationID == route.ApplicationID &&
		existing.Env == route.Env &&
		existing.RouteKey == route.RouteKey
}

// validateAccumulatedGatewayRoutes catches duplicate route_key / host+path
// across the full batch of routes submitted in one syncTargets call.
func validateAccumulatedGatewayRoutes(routesByEnv map[domain.Environment][]*domain.ApplicationGatewayRoute) error {
	seenKeys := make(map[string]struct{})
	seenHostPaths := make(map[string]struct{})
	for _, routes := range routesByEnv {
		for _, route := range routes {
			if route.RouteKey != "" {
				if _, ok := seenKeys[route.RouteKey]; ok {
					return errors.New(fmt.Sprintf("duplicate gateway route key %q", route.RouteKey))
				}
				seenKeys[route.RouteKey] = struct{}{}
			}
			if route.Host != "" {
				hostPath := route.Host + "\x00" + route.PathPrefix
				if _, ok := seenHostPaths[hostPath]; ok {
					return errors.New(fmt.Sprintf("duplicate gateway host/path %s%s", route.Host, route.PathPrefix))
				}
				seenHostPaths[hostPath] = struct{}{}
			}
		}
	}
	return nil
}

func (s *ApplicationService) attachTargets(ctx context.Context, app *domain.Application) (*domain.Application, error) {
	targets, err := s.targets.ListByApplication(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	if err := s.attachGatewayRoutesToTargets(ctx, app, targets); err != nil {
		return nil, err
	}
	if err := s.attachHealthToTargets(ctx, targets); err != nil {
		return nil, err
	}
	app.Targets = targets
	return app, nil
}

func (s *ApplicationService) attachTargetsToApps(ctx context.Context, apps []*domain.Application) ([]*domain.Application, error) {
	ids := make([]int64, 0, len(apps))
	for _, app := range apps {
		ids = append(ids, app.ID)
	}
	targets, err := s.targets.ListByApplicationIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	routes, err := s.routes.ListByApplicationIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	if err := s.attachRouteBackends(ctx, routes); err != nil {
		return nil, err
	}
	routesByAppEnv := make(map[string][]*domain.ApplicationGatewayRoute, len(routes))
	for _, route := range routes {
		key := appEnvKey(route.ApplicationID, route.Env)
		routesByAppEnv[key] = append(routesByAppEnv[key], route)
	}
	byApp := make(map[int64][]*domain.ApplicationEnvTarget, len(apps))
	for _, target := range targets {
		target.GatewayRoutes = routesByAppEnv[appEnvKey(target.ApplicationID, target.Env)]
		byApp[target.ApplicationID] = append(byApp[target.ApplicationID], target)
	}
	if err := s.attachHealthToTargets(ctx, targets); err != nil {
		return nil, err
	}
	for _, app := range apps {
		app.Targets = byApp[app.ID]
	}
	return apps, nil
}

func (s *ApplicationService) attachHealthToTargets(ctx context.Context, targets []*domain.ApplicationEnvTarget) error {
	if len(targets) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, target.ID)
	}
	rows, err := s.health.ListByTargetIDs(ctx, ids)
	if err != nil {
		return err
	}
	byTarget := make(map[int64]*domain.ApplicationInstanceHealth, len(rows))
	for _, row := range rows {
		byTarget[row.TargetID] = row
	}
	for _, target := range targets {
		target.Health = byTarget[target.ID]
	}
	return nil
}

func (s *ApplicationService) attachRouteBackends(ctx context.Context, routes []*domain.ApplicationGatewayRoute) error {
	if len(routes) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(routes))
	for _, route := range routes {
		ids = append(ids, route.ID)
	}
	rows, err := s.routeBackends.ListByRouteIDs(ctx, ids)
	if err != nil {
		return err
	}
	byRoute := make(map[int64][]*domain.ApplicationGatewayRouteBackend, len(routes))
	for _, row := range rows {
		byRoute[row.RouteID] = append(byRoute[row.RouteID], row)
	}
	for _, route := range routes {
		route.Backends = byRoute[route.ID]
	}
	return nil
}

func (s *ApplicationService) attachGatewayRoutesToTargets(ctx context.Context, app *domain.Application, targets []*domain.ApplicationEnvTarget) error {
	routes, err := s.routes.ListByApplication(ctx, app.ID)
	if err != nil {
		return err
	}
	if err := s.attachRouteBackends(ctx, routes); err != nil {
		return err
	}
	routesByEnv := make(map[domain.Environment][]*domain.ApplicationGatewayRoute, len(routes))
	for _, route := range routes {
		routesByEnv[route.Env] = append(routesByEnv[route.Env], route)
	}
	for _, target := range targets {
		target.GatewayRoutes = routesByEnv[target.Env]
	}
	return nil
}

func appEnvKey(appID int64, env domain.Environment) string {
	return fmt.Sprintf("%d:%s", appID, env)
}
