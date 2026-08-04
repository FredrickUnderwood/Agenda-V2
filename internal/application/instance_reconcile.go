package application

import (
	"bytes"
	"context"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/pipeline"
)

// The narrow dependency slices InstanceReconcile needs. Kept as interfaces (the
// concrete repositories / *pipeline.Builder satisfy them structurally) so this
// orchestrator is unit-testable without a database and so the application layer
// stays free of a direct repository import, matching the rest of the package.
type (
	stoppedTargetLister interface {
		ListStoppedByMachine(ctx context.Context, machineID int64) ([]*domain.ApplicationEnvTarget, error)
	}
	reconcileAppGetter interface {
		GetByID(ctx context.Context, id int64) (*domain.Application, error)
	}
	reconcileReleaseGetter interface {
		GetLatestVerified(ctx context.Context, appID int64, env domain.Environment, instanceName string) (*domain.ApplicationRelease, error)
	}
	// containerTeardownBuilder assembles just the compose_down step for an
	// instance — no gateway drain, which already ran (and succeeded, since it
	// reaches the gateway, not the machine) when the decommission was requested.
	containerTeardownBuilder interface {
		BuildContainerTeardownStep(ctx context.Context, target *domain.DeployTarget) (pipeline.Blueprint, string, error)
	}
)

// InstanceReconcile finishes container teardowns that a machine was offline for.
// When an operator decommissions an instance, the gateway is drained immediately
// and the intent is persisted as DesiredState=stopped, but the actual `docker`
// teardown fails if the machine is unreachable. MachineMonitor calls
// ReconcileStopped when a node comes back online, re-attempting the teardown for
// every still-stopped instance on it.
//
// It records no DeployLog: this is an idempotent background self-heal (the
// teardown script is a no-op once the containers are gone), the same shape as
// ProxyResyncService, so it logs its outcome rather than minting an
// operator-facing deploy record on every recovery.
type InstanceReconcile struct {
	targets  stoppedTargetLister
	builder  containerTeardownBuilder
	apps     reconcileAppGetter
	releases reconcileReleaseGetter
}

func NewInstanceReconcile(
	targets stoppedTargetLister,
	builder containerTeardownBuilder,
	apps reconcileAppGetter,
	releases reconcileReleaseGetter,
) *InstanceReconcile {
	return &InstanceReconcile{targets: targets, builder: builder, apps: apps, releases: releases}
}

// ReconcileStopped re-runs the container teardown for every decommissioned
// instance on machineID, returning how many were successfully torn down. A
// per-instance failure is logged and skipped rather than aborting the loop, so
// one unreachable/misconfigured instance can't block reconciling the others.
func (r *InstanceReconcile) ReconcileStopped(ctx context.Context, machineID int64) (int, error) {
	targets, err := r.targets.ListStoppedByMachine(ctx, machineID)
	if err != nil {
		return 0, err
	}
	if len(targets) == 0 {
		return 0, nil
	}

	reconciled := 0
	appCache := make(map[int64]*domain.Application)
	for _, t := range targets {
		app, ok := appCache[t.ApplicationID]
		if !ok {
			app, err = r.apps.GetByID(ctx, t.ApplicationID)
			if err != nil || app == nil {
				// Stale target referencing a deleted app (no-FK schema): nothing to
				// tear down under a name we can't resolve, so skip.
				logger.L().Warn("instance reconcile: cannot resolve application; skipping",
					zap.Int64("machine_id", machineID), zap.Int64("application_id", t.ApplicationID),
					zap.String("instance", t.InstanceName), zap.Error(err))
				continue
			}
			appCache[t.ApplicationID] = app
		}

		// Resolve the instance's last running branch (best-effort) so teardown can
		// also clean the branch-specific compose project/network; label-based
		// removal is branch-independent, so an unknown branch is fine.
		branch := ""
		if rel, relErr := r.releases.GetLatestVerified(ctx, t.ApplicationID, t.Env, t.InstanceName); relErr == nil && rel != nil {
			branch = rel.Branch
		}

		dt := &domain.DeployTarget{App: app, EnvTarget: t, Branch: branch}
		step, _, buildErr := r.builder.BuildContainerTeardownStep(ctx, dt)
		if buildErr != nil {
			logger.L().Warn("instance reconcile: cannot build teardown step; skipping",
				zap.Int64("machine_id", machineID), zap.String("instance", t.InstanceName), zap.Error(buildErr))
			continue
		}

		var buf bytes.Buffer
		rc := &pipeline.RunContext{App: app, Branch: branch, Output: &buf}
		if execErr := step.Exec.Execute(ctx, rc); execErr != nil {
			logger.L().Warn("instance reconcile: teardown failed; will retry on next recovery",
				zap.Int64("machine_id", machineID), zap.String("instance", t.InstanceName),
				zap.String("output", buf.String()), zap.Error(execErr))
			continue
		}
		reconciled++
	}

	if reconciled > 0 {
		logger.L().Info("instance reconcile: tore down stopped instances after machine recovery",
			zap.Int64("machine_id", machineID), zap.Int("count", reconciled))
	}
	return reconciled, nil
}
