package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/pipeline"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
	alertsdk "github.com/FredrickUnderwood/agenda-v2/sdk/go/alert"
)

// InstanceLifecycleApplication owns runtime lifecycle actions on a single
// instance that are not deploys: decommission (drain gateway traffic + tear the
// containers down, recording the intent as DesiredState=stopped) and recommission
// (clear that intent). It reuses the exact same machinery a deploy uses — the
// pipeline Runner, DeployLog audit trail, and the per (app, env, instance) deploy
// lock — so a teardown and a deploy of the same instance can never race, and the
// teardown shows up in the deploy-log UI with captured `docker` output.
//
// It is deliberately separate from ReleaseApplication: a decommission has no
// ApplicationRelease and no release state machine to advance. The DeployLog it
// creates carries ReleaseID=0.
// healthClearer drops an instance's stale health record on decommission, so a
// stopped instance stops reading green in every health view. Narrow interface,
// optional/nil-safe (satisfied by *service.ApplicationHealthService).
type healthClearer interface {
	ClearTargetHealth(ctx context.Context, targetID int64) error
}

type InstanceLifecycleApplication struct {
	cfg        *config.Config
	builder    *pipeline.Builder
	runner     *pipeline.Runner
	logSvc     *service.DeployLogService
	stepSvc    *service.PipelineStepService
	appSvc     *service.ApplicationService
	releaseSvc *service.ApplicationReleaseService
	lockSvc    *service.DeployLockService
	// health clears an instance's stale health row on decommission. Optional/nil-safe.
	health healthClearer
	// alerts fires an ops notification (+ inbox) when a teardown run fails.
	// Optional/nil-safe.
	alerts *service.AlertService
}

func NewInstanceLifecycleApplication(
	cfg *config.Config,
	builder *pipeline.Builder,
	runner *pipeline.Runner,
	logSvc *service.DeployLogService,
	stepSvc *service.PipelineStepService,
	appSvc *service.ApplicationService,
	releaseSvc *service.ApplicationReleaseService,
	lockSvc *service.DeployLockService,
	health healthClearer,
	alerts *service.AlertService,
) *InstanceLifecycleApplication {
	return &InstanceLifecycleApplication{
		cfg: cfg, builder: builder, runner: runner,
		logSvc: logSvc, stepSvc: stepSvc, appSvc: appSvc,
		releaseSvc: releaseSvc, lockSvc: lockSvc, health: health, alerts: alerts,
	}
}

// Decommission drains an instance's gateway traffic and tears its containers
// down, recording DesiredState=stopped as durable intent. It returns
// immediately with the created DeployLog (like Deploy returns 202); the drain +
// container removal run asynchronously in a goroutine holding the instance's
// deploy lock.
//
// DesiredState=stopped is persisted before the goroutine starts, so the health
// monitor stops probing the instance and the gateway backend resolver excludes
// it right away — even if the container teardown later fails (e.g. the machine
// is offline), the intent is durable and the drain has already removed inbound
// traffic. A failed teardown is safe to retry: the teardown script is idempotent.
func (a *InstanceLifecycleApplication) Decommission(ctx context.Context, appID, targetID int64) (*domain.DeployLog, error) {
	logger.L().Info("lifecycle: decommission requested", zap.Int64("application_id", appID), zap.Int64("target_id", targetID))
	app, target, err := a.loadTarget(ctx, appID, targetID)
	if err != nil {
		return nil, err
	}

	// Resolve the instance's current running branch (best-effort) so the teardown
	// can also clean up the branch-specific compose project/network. An unknown
	// branch is fine — the label-based container removal is branch-independent.
	branch := a.currentBranch(ctx, appID, target.Env, target.InstanceName)
	// Mark stopped in-memory too, so the drain's sibling resolution excludes this
	// instance defensively regardless of read-after-write visibility.
	target.DesiredState = domain.RuntimeStateStopped
	dt := &domain.DeployTarget{App: app, EnvTarget: target, Branch: branch}

	lockKey := service.ReleaseLockKey(appID, string(target.Env), target.InstanceName)
	token, err := a.lockSvc.AcquireKey(ctx, lockKey, a.lockTTL())
	if err != nil {
		return nil, err
	}

	// Persist the intent before doing anything physical: durable even if the
	// process dies mid-teardown, and it immediately stops health probing.
	if err := a.appSvc.SetInstanceDesiredState(ctx, targetID, domain.RuntimeStateStopped); err != nil {
		a.lockSvc.ReleaseKey(ctx, lockKey, token)
		return nil, err
	}

	// Drop the stale health row so the instance stops reading green everywhere
	// (instance list + gateway route map both render off this record, and the
	// monitor won't touch a stopped instance again to correct it). Best-effort:
	// a failure here must not block the teardown — the intent is already durable.
	if a.health != nil {
		if err := a.health.ClearTargetHealth(ctx, targetID); err != nil {
			logger.L().Warn("lifecycle: failed to clear instance health on decommission",
				zap.Int64("target_id", targetID), zap.Error(err))
		}
	}

	blueprints, localPath, err := a.builder.BuildTeardown(ctx, dt)
	if err != nil {
		a.lockSvc.ReleaseKey(ctx, lockKey, token)
		return nil, err
	}
	log, err := a.prepareTeardownLog(ctx, dt, blueprints)
	if err != nil {
		a.lockSvc.ReleaseKey(ctx, lockKey, token)
		return nil, err
	}

	go func() {
		defer a.lockSvc.ReleaseKey(context.Background(), lockKey, token)
		a.runTeardown(dt, log, blueprints, localPath)
	}()
	return log, nil
}

// Recommission clears the stopped intent so the instance rejoins the deploy set
// and health monitoring. It does NOT bring containers back up — the caller
// triggers a normal deploy for that; recommission only makes the instance
// eligible again. Idempotent: recommissioning a running instance is a no-op.
func (a *InstanceLifecycleApplication) Recommission(ctx context.Context, appID, targetID int64) (*domain.ApplicationEnvTarget, error) {
	logger.L().Info("lifecycle: recommission requested", zap.Int64("application_id", appID), zap.Int64("target_id", targetID))
	target, err := a.appSvc.GetTargetByID(ctx, appID, targetID)
	if err != nil {
		return nil, err
	}
	if !target.Stopped() {
		return target, nil
	}
	if err := a.appSvc.SetInstanceDesiredState(ctx, targetID, domain.RuntimeStateRunning); err != nil {
		return nil, err
	}
	target.DesiredState = domain.RuntimeStateRunning
	return target, nil
}

// loadTarget resolves the application and the fully-attached target (gateway
// routes + backends + health) for a decommission, without the Enabled gate
// GetTargetForEnvInstance applies — an instance that was disabled but still has
// live containers must still be tear-down-able. ListTargetsByApplication is the
// same attach path the drain's sibling resolver uses, keeping the two consistent.
func (a *InstanceLifecycleApplication) loadTarget(ctx context.Context, appID, targetID int64) (*domain.Application, *domain.ApplicationEnvTarget, error) {
	basic, err := a.appSvc.GetTargetByID(ctx, appID, targetID)
	if err != nil {
		return nil, nil, err
	}
	app, err := a.appSvc.Get(ctx, appID)
	if err != nil {
		return nil, nil, err
	}
	targets, err := a.appSvc.ListTargetsByApplication(ctx, appID, basic.Env)
	if err != nil {
		return nil, nil, err
	}
	for _, t := range targets {
		if t.ID == targetID {
			return app, t, nil
		}
	}
	return nil, nil, errors.New(fmt.Sprintf("instance %d not found for application %d", targetID, appID))
}

func (a *InstanceLifecycleApplication) currentBranch(ctx context.Context, appID int64, env domain.Environment, instance string) string {
	rel, err := a.releaseSvc.GetLatestVerified(ctx, appID, env, instance)
	if err != nil || rel == nil {
		return ""
	}
	return rel.Branch
}

// prepareTeardownLog persists the DeployLog (ReleaseID=0) and its step rows for
// a teardown run — the teardown analogue of ReleaseApplication.prepareRun.
func (a *InstanceLifecycleApplication) prepareTeardownLog(ctx context.Context, target *domain.DeployTarget, blueprints []pipeline.Blueprint) (*domain.DeployLog, error) {
	log := &domain.DeployLog{
		ApplicationID:  target.App.ID,
		ReleaseID:      0,
		Env:            target.Env(),
		DeployTargetID: target.EnvTarget.ID,
		InstanceName:   target.EnvTarget.InstanceName,
		MachineID:      target.EnvTarget.MachineID,
		DeployPort:     target.EnvTarget.Port,
		Branch:         target.Branch,
		Status:         domain.DeployStatusPending,
	}
	if err := a.logSvc.Create(ctx, log); err != nil {
		return nil, err
	}
	steps := pipeline.BlueprintToSteps(log.ID, blueprints)
	if err := a.stepSvc.CreateBatch(ctx, steps); err != nil {
		return nil, err
	}
	log.Steps = steps
	return log, nil
}

func (a *InstanceLifecycleApplication) runTeardown(target *domain.DeployTarget, log *domain.DeployLog, blueprints []pipeline.Blueprint, localPath string) {
	timeout := a.cfg.Deploy.DefaultTimeout.Duration
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	a.runner.Run(ctx, log, target, blueprints, localPath)
	if log.Status == domain.DeployStatusFailed {
		a.alertTeardownFailed(context.WithoutCancel(ctx), target, log)
	}
}

// alertTeardownFailed mirrors ReleaseApplication.alertDeployFailed: a failed
// teardown leaves containers possibly still running and gateway state possibly
// half-drained, so it is worth surfacing to ops. Nil-safe and best-effort; it
// never affects the outcome and bounds its own send.
func (a *InstanceLifecycleApplication) alertTeardownFailed(ctx context.Context, target *domain.DeployTarget, log *domain.DeployLog) {
	if a.alerts == nil || target == nil {
		return
	}
	appName, env, instance := "unknown", "", ""
	if target.App != nil {
		appName = target.App.Name
	}
	env = string(target.Env())
	if target.EnvTarget != nil {
		instance = target.EnvTarget.InstanceName
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Decommission failed for %s (%s/%s).\n", appName, env, instance)
	fmt.Fprintf(&b, "The instance may still have running containers or partially drained gateway routes — inspect and retry.\n")
	if log != nil {
		fmt.Fprintf(&b, "Deploy log: #%d", log.ID)
	}

	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	a.alerts.SendToAll(sendCtx, alertsdk.Message{
		Title:   fmt.Sprintf("Decommission failed: %s (%s)", appName, env),
		Content: b.String(),
		Level:   alertsdk.LevelWarning,
	})
}

func (a *InstanceLifecycleApplication) lockTTL() time.Duration {
	ttl := a.cfg.Deploy.DefaultTimeout.Duration
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	return ttl + 30*time.Second
}
