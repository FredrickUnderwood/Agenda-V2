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
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
)

type ApplicationHealthService struct {
	targets  *repository.ApplicationTargetRepository
	health   *repository.ApplicationInstanceHealthRepository
	machines *repository.MachineRepository
	client   *http.Client
}

func NewApplicationHealthService(
	targets *repository.ApplicationTargetRepository,
	health *repository.ApplicationInstanceHealthRepository,
	machines *repository.MachineRepository,
) *ApplicationHealthService {
	return &ApplicationHealthService{
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
	checkURL, err := s.resolveHealthCheckURL(ctx, target)
	if err != nil {
		return nil, err
	}

	timeout := time.Duration(target.HealthCheckTimeoutMS) * time.Millisecond
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, target.HealthCheckMethod, checkURL, nil)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	resp, err := s.client.Do(req)
	latency := time.Since(started)
	now := time.Now()

	var httpStatus int
	var checkErr error
	if err != nil {
		checkErr = err
	} else {
		httpStatus = resp.StatusCode
		_ = resp.Body.Close()
		if resp.StatusCode != target.HealthCheckExpectedStatus {
			checkErr = errors.New(fmt.Sprintf("unexpected HTTP status %d, want %d", resp.StatusCode, target.HealthCheckExpectedStatus))
		}
	}

	prev, err := s.health.GetByTargetID(ctx, target.ID)
	if err != nil {
		return nil, err
	}
	next := buildHealthResult(target, prev, now, int(latency.Milliseconds()), httpStatus, checkErr)
	if err := s.health.Upsert(ctx, next); err != nil {
		return nil, err
	}
	return next, nil
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

func (s *ApplicationHealthService) resolveHealthCheckURL(ctx context.Context, target *domain.ApplicationEnvTarget) (string, error) {
	if rawURL := strings.TrimSpace(target.HealthCheckURL); rawURL != "" {
		u, err := url.Parse(rawURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return "", errors.New(fmt.Sprintf("%s/%s health check url is invalid", target.Env, target.InstanceName))
		}
		return rawURL, nil
	}
	if target.Port <= 0 {
		return "", errors.New(fmt.Sprintf("%s/%s health check target port is required", target.Env, target.InstanceName))
	}
	host := strings.TrimSpace(target.HealthCheckHost)
	var machine *domain.Machine
	if host == "" && target.MachineID > 0 {
		m, err := s.machines.GetByID(ctx, target.MachineID)
		if err != nil {
			return "", err
		}
		machine = m
		host = strings.TrimSpace(machine.Host)
	}
	if host == "" {
		host = "127.0.0.1"
		// Agent-mode machines run the deploy target's container via a
		// separate agenda-node process/container (typically docker-outside-
		// of-docker), never inside control-plane's own container — so
		// control-plane's 127.0.0.1 can never reach it. host.docker.internal
		// reaches whatever the target published on the physical host,
		// mirroring the same convention already used for
		// gateway.backend_host (internal/pipeline/builder.go). Requires
		// control-plane's own container to have
		// `extra_hosts: host.docker.internal:host-gateway` configured.
		if machine != nil && machine.Mode == domain.MachineModeAgent {
			host = "host.docker.internal"
		}
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
