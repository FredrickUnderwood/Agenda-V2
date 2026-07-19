package service

import (
	"context"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/nodeproxy"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
)

// proxyTargetLister and verifiedReleaseGetter are the narrow repository slices
// ProxyResyncService needs, kept as interfaces so it is unit-testable without a
// database.
type proxyTargetLister interface {
	ListEnabledByMachine(ctx context.Context, machineID int64) ([]*domain.ApplicationEnvTarget, error)
}

type verifiedReleaseGetter interface {
	GetLatestVerified(ctx context.Context, appID int64, env domain.Environment, instanceName string) (*domain.ApplicationRelease, error)
}

// ProxyResyncService re-registers a machine's agenda-node reverse-proxy routes
// (instance name → current local port).
//
// The node's ProxyRegistry is in-memory and cleared whenever the node process
// restarts, but the control plane otherwise only (re)registers routes during a
// deploy's gateway_routes_sync step. So after an unattended node restart every
// instance on it 502s ("unknown instance") until its next deploy. ResyncMachine
// closes that gap; MachineMonitor calls it when a node transitions back online.
type ProxyResyncService struct {
	targets  proxyTargetLister
	releases verifiedReleaseGetter
	machines machineGetter
	// register is nodeproxy.RegisterProxyTarget in production; overridable in
	// tests so the resync logic can be verified without a live node.
	register func(ctx context.Context, agentBaseURL, agentToken, instanceName string, port int) error
}

func NewProxyResyncService(
	targets *repository.ApplicationTargetRepository,
	releases *repository.ApplicationReleaseRepository,
	machines machineGetter,
) *ProxyResyncService {
	return &ProxyResyncService{
		targets:  targets,
		releases: releases,
		machines: machines,
		register: nodeproxy.RegisterProxyTarget,
	}
}

// ResyncMachine re-registers the proxy route for every enabled, verified
// instance on machineID with that machine's agenda-node, and returns how many
// were successfully (re)registered.
//
// Non-agent machines have no resident node proxy and are a no-op. Only
// instances with a verified release are considered — that mirrors exactly what
// gateway_routes_sync would have registered, so recovery can't resurrect an
// instance that was never actually deployed. A per-instance failure is logged
// and skipped rather than aborting the loop, so one unreachable/misconfigured
// instance can't block re-registering the healthy ones.
func (s *ProxyResyncService) ResyncMachine(ctx context.Context, machineID int64) (int, error) {
	machine, err := s.machines.Get(ctx, machineID)
	if err != nil {
		return 0, err
	}
	mc := ToMachineConfig(machine)
	if !mc.IsAgent() {
		return 0, nil
	}

	targets, err := s.targets.ListEnabledByMachine(ctx, machineID)
	if err != nil {
		return 0, err
	}

	registered := 0
	for _, t := range targets {
		if t.Port <= 0 {
			continue
		}
		rel, err := s.releases.GetLatestVerified(ctx, t.ApplicationID, t.Env, t.InstanceName)
		if err != nil || rel == nil {
			continue
		}
		instance := domain.NormalizeInstanceName(t.InstanceName)
		if err := s.register(ctx, mc.AgentBaseURL, mc.AgentToken, instance, t.Port); err != nil {
			logger.L().Warn("proxy resync: failed to register instance",
				zap.Int64("machine_id", machineID), zap.String("instance", instance),
				zap.Int("port", t.Port), zap.Error(err))
			continue
		}
		registered++
	}

	if registered > 0 {
		logger.L().Info("proxy resync: re-registered instances after node recovery",
			zap.Int64("machine_id", machineID), zap.Int("count", registered))
	}
	return registered, nil
}
