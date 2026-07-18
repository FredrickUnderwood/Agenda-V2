package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/nodeproxy"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
)

type ApplicationHealthService struct {
	apps     *repository.ApplicationRepository
	targets  *repository.ApplicationTargetRepository
	health   *repository.ApplicationInstanceHealthRepository
	machines machineGetter
	client   *http.Client
}

func NewApplicationHealthService(
	apps *repository.ApplicationRepository,
	targets *repository.ApplicationTargetRepository,
	health *repository.ApplicationInstanceHealthRepository,
	machines machineGetter,
) *ApplicationHealthService {
	return &ApplicationHealthService{
		apps:     apps,
		targets:  targets,
		health:   health,
		machines: machines,
		client:   &http.Client{},
	}
}

func (s *ApplicationHealthService) GetTargetHealthForApplication(ctx context.Context, appID, targetID int64) (*domain.ApplicationInstanceHealth, error) {
	target, err := s.targets.GetByID(ctx, targetID)
	if err != nil {
		return nil, err
	}
	if target.ApplicationID != appID {
		return nil, errors.New(fmt.Sprintf("application %d target %d not found", appID, targetID))
	}
	return s.health.GetByTargetID(ctx, targetID)
}

func (s *ApplicationHealthService) CheckTargetForApplication(ctx context.Context, appID, targetID int64) (*domain.ApplicationInstanceHealth, error) {
	target, err := s.targets.GetByID(ctx, targetID)
	if err != nil {
		return nil, err
	}
	if target.ApplicationID != appID {
		return nil, errors.New(fmt.Sprintf("application %d target %d not found", appID, targetID))
	}
	return s.CheckTarget(ctx, target)
}

func (s *ApplicationHealthService) CheckTarget(ctx context.Context, target *domain.ApplicationEnvTarget) (*domain.ApplicationInstanceHealth, error) {
	if target == nil {
		return nil, errors.New("target is nil")
	}
	normalizeHealthCheckConfig(target)
	if !target.Enabled {
		return nil, errors.New(fmt.Sprintf("%s/%s target is disabled", target.Env, target.InstanceName))
	}
	if !target.HealthCheckEnabled {
		return nil, errors.New(fmt.Sprintf("%s/%s health check is disabled", target.Env, target.InstanceName))
	}
	if target.HealthCheckType != "http" {
		return nil, errors.New(fmt.Sprintf("health check type %q is not supported", target.HealthCheckType))
	}

	httpStatus, latencyMS, checkErr := s.probe(ctx, target)
	now := time.Now()

	prev, err := s.health.GetByTargetID(ctx, target.ID)
	if err != nil {
		return nil, err
	}
	next := buildHealthResult(target, prev, now, latencyMS, httpStatus, checkErr)
	if err := s.health.Upsert(ctx, next); err != nil {
		return nil, err
	}
	return next, nil
}

// probe runs one health check and returns the app's HTTP status, the probe
// latency in ms, and a non-nil checkErr when the app was unreachable or
// returned an unexpected status. It picks the reachability path by machine
// mode: agent-mode machines' app ports live on a (possibly remote) node the
// control plane cannot reach directly, so the probe is relayed through the
// node's management API — exactly as metrics scraping is (see
// ApplicationMetricsService). ssh/local machines (and an explicit
// HealthCheckHost / HealthCheckURL override) are probed directly.
func (s *ApplicationHealthService) probe(ctx context.Context, target *domain.ApplicationEnvTarget) (httpStatus, latencyMS int, checkErr error) {
	// An operator-supplied full URL is expected to be reachable from the
	// control plane as-is; probe it directly regardless of machine mode.
	if rawURL := strings.TrimSpace(target.HealthCheckURL); rawURL != "" {
		u, err := url.Parse(rawURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return 0, 0, errors.New(fmt.Sprintf("%s/%s health check url is invalid", target.Env, target.InstanceName))
		}
		return s.probeDirect(ctx, target, rawURL)
	}
	if target.Port <= 0 {
		return 0, 0, errors.New(fmt.Sprintf("%s/%s health check target port is required", target.Env, target.InstanceName))
	}

	var machine *domain.Machine
	if target.MachineID > 0 {
		// Get (not the raw repo) so AgentToken is decrypted — probeViaNode
		// presents it to the node, exactly as the logs/metrics relays do.
		m, err := s.machines.Get(ctx, target.MachineID)
		if err != nil {
			return 0, 0, err
		}
		machine = m
	}

	// Agent mode with no explicit host override → relay through the node.
	if machine != nil && machine.Mode == domain.MachineModeAgent && strings.TrimSpace(target.HealthCheckHost) == "" {
		return s.probeViaNode(ctx, target, machine)
	}

	checkURL, err := directHealthCheckURL(target, machine)
	if err != nil {
		return 0, 0, err
	}
	return s.probeDirect(ctx, target, checkURL)
}

// probeDirect issues the health request straight from the control plane. Used
// for ssh/local machines and explicit host/url overrides.
func (s *ApplicationHealthService) probeDirect(ctx context.Context, target *domain.ApplicationEnvTarget, checkURL string) (httpStatus, latencyMS int, checkErr error) {
	timeout := time.Duration(target.HealthCheckTimeoutMS) * time.Millisecond
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, target.HealthCheckMethod, checkURL, nil)
	if err != nil {
		return 0, 0, err
	}
	started := time.Now()
	resp, err := s.client.Do(req)
	latencyMS = int(time.Since(started).Milliseconds())
	if err != nil {
		return 0, latencyMS, err
	}
	httpStatus = resp.StatusCode
	_ = resp.Body.Close()
	if httpStatus != target.HealthCheckExpectedStatus {
		return httpStatus, latencyMS, errors.New(fmt.Sprintf("unexpected HTTP status %d, want %d", httpStatus, target.HealthCheckExpectedStatus))
	}
	return httpStatus, latencyMS, nil
}

// probeViaNode asks the target's agenda-node to probe the app locally and relay
// the result. A transport error to the node itself (node offline/unreachable)
// surfaces as checkErr, so an offline node correctly drives its instances
// unhealthy — the very failure mode that a direct host.docker.internal probe
// silently masked.
func (s *ApplicationHealthService) probeViaNode(ctx context.Context, target *domain.ApplicationEnvTarget, machine *domain.Machine) (httpStatus, latencyMS int, checkErr error) {
	appName := strconv.FormatInt(target.ApplicationID, 10)
	if s.apps != nil {
		if app, err := s.apps.GetByID(ctx, target.ApplicationID); err == nil {
			appName = app.Name
		}
	}
	res, err := nodeproxy.Probe(
		ctx, machine.AgentBaseURL, machine.AgentToken,
		appName, target.InstanceName,
		target.HealthCheckScheme, target.HealthCheckMethod, target.HealthCheckPath,
		target.Port, target.HealthCheckTimeoutMS,
	)
	if err != nil {
		return 0, 0, err
	}
	if res.Error != "" {
		return res.HTTPStatus, res.LatencyMS, errors.New(res.Error)
	}
	if res.HTTPStatus != target.HealthCheckExpectedStatus {
		return res.HTTPStatus, res.LatencyMS, errors.New(fmt.Sprintf("unexpected HTTP status %d, want %d", res.HTTPStatus, target.HealthCheckExpectedStatus))
	}
	return res.HTTPStatus, res.LatencyMS, nil
}

func (s *ApplicationHealthService) CheckDueTargets(ctx context.Context) error {
	targets, err := s.targets.ListHealthCheckEnabled(ctx)
	if err != nil {
		return err
	}
	targetIDs := make([]int64, 0, len(targets))
	for _, target := range targets {
		targetIDs = append(targetIDs, target.ID)
	}
	rows, err := s.health.ListByTargetIDs(ctx, targetIDs)
	if err != nil {
		return err
	}
	healthByTarget := make(map[int64]*domain.ApplicationInstanceHealth, len(rows))
	for _, row := range rows {
		healthByTarget[row.TargetID] = row
	}
	now := time.Now()
	for _, target := range targets {
		normalizeHealthCheckConfig(target)
		if !healthCheckDue(now, target, healthByTarget[target.ID]) {
			continue
		}
		_, _ = s.CheckTarget(ctx, target)
	}
	return nil
}

// directHealthCheckURL builds the URL for a control-plane-issued probe against
// an ssh/local machine (or a target with an explicit HealthCheckHost). Agent-
// mode targets never reach here — they are relayed through the node instead,
// so there is no host.docker.internal single-machine assumption to make.
func directHealthCheckURL(target *domain.ApplicationEnvTarget, machine *domain.Machine) (string, error) {
	if target.Port <= 0 {
		return "", errors.New(fmt.Sprintf("%s/%s health check target port is required", target.Env, target.InstanceName))
	}
	host := strings.TrimSpace(target.HealthCheckHost)
	if host == "" && machine != nil {
		host = strings.TrimSpace(machine.Host)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	hostPort := host
	if _, _, err := net.SplitHostPort(host); err != nil {
		hostPort = net.JoinHostPort(host, strconv.Itoa(target.Port))
	}
	return target.HealthCheckScheme + "://" + hostPort + target.HealthCheckPath, nil
}

func buildHealthResult(
	target *domain.ApplicationEnvTarget,
	prev *domain.ApplicationInstanceHealth,
	now time.Time,
	latencyMS int,
	httpStatus int,
	checkErr error,
) *domain.ApplicationInstanceHealth {
	status := domain.HealthStatusUnknown
	successes := 0
	failures := 0
	var lastSuccessAt *time.Time
	var lastFailureAt *time.Time
	if prev != nil {
		status = prev.Status
		successes = prev.ConsecutiveSuccesses
		failures = prev.ConsecutiveFailures
		lastSuccessAt = prev.LastSuccessAt
		lastFailureAt = prev.LastFailureAt
	}
	errorMsg := ""
	if checkErr == nil {
		successes++
		failures = 0
		lastSuccessAt = &now
		if successes >= target.HealthCheckSuccessThreshold {
			status = domain.HealthStatusHealthy
		}
	} else {
		failures++
		successes = 0
		lastFailureAt = &now
		errorMsg = checkErr.Error()
		if failures >= target.HealthCheckFailureThreshold {
			status = domain.HealthStatusUnhealthy
		}
	}
	return &domain.ApplicationInstanceHealth{
		TargetID:             target.ID,
		ApplicationID:        target.ApplicationID,
		Env:                  target.Env,
		InstanceName:         target.InstanceName,
		Status:               status,
		CheckedAt:            &now,
		HTTPStatus:           httpStatus,
		LatencyMS:            latencyMS,
		ErrorMsg:             errorMsg,
		ConsecutiveSuccesses: successes,
		ConsecutiveFailures:  failures,
		LastSuccessAt:        lastSuccessAt,
		LastFailureAt:        lastFailureAt,
	}
}

func healthCheckDue(now time.Time, target *domain.ApplicationEnvTarget, health *domain.ApplicationInstanceHealth) bool {
	if health == nil || health.CheckedAt == nil {
		return true
	}
	interval := time.Duration(target.HealthCheckIntervalSec) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return now.Sub(*health.CheckedAt) >= interval
}
