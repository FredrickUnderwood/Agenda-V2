package application

import (
	"context"
	"errors"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/agenda-v2/config"
	"github.com/agenda-v2/internal/domain"
	"github.com/agenda-v2/internal/logger"
	"github.com/agenda-v2/internal/pipeline"
	"github.com/agenda-v2/internal/service"
)

// ReleaseApplication is the sole deploy-trigger orchestrator in this build:
// every physical pipeline run is driven by a release. It owns the coupling
// between ApplicationRelease's status machine (service.ApplicationReleaseService)
// and the underlying DeployLog pipeline execution (pipeline.Runner) — building
// the target, acquiring a per (application, env, instance) lock, running the
// pipeline, and syncing the release status once the run reaches a terminal
// state.
type ReleaseApplication struct {
	cfg        *config.Config
	builder    *pipeline.Builder
	runner     *pipeline.Runner
	logSvc     *service.DeployLogService
	stepSvc    *service.PipelineStepService
	appSvc     *service.ApplicationService
	releaseSvc *service.ApplicationReleaseService
	lockSvc    *service.DeployLockService
}

func NewReleaseApplication(
	cfg *config.Config,
	builder *pipeline.Builder,
	runner *pipeline.Runner,
	logSvc *service.DeployLogService,
	stepSvc *service.PipelineStepService,
	appSvc *service.ApplicationService,
	releaseSvc *service.ApplicationReleaseService,
	lockSvc *service.DeployLockService,
) *ReleaseApplication {
	return &ReleaseApplication{
		cfg: cfg, builder: builder, runner: runner,
		logSvc: logSvc, stepSvc: stepSvc, appSvc: appSvc,
		releaseSvc: releaseSvc, lockSvc: lockSvc,
	}
}

// Deploy builds the pipeline for a draft/failed release, persists the
// DeployLog + step rows, flips the release to "deploying", and runs the
// pipeline in a background goroutine. Returns immediately with the created
// DeployLog so callers (the HTTP handler) can return 202.
func (a *ReleaseApplication) Deploy(ctx context.Context, releaseID int64) (*domain.DeployLog, error) {
	logger.L().Info("release: deploy requested", zap.Int64("release_id", releaseID))
	rel, target, err := a.loadTarget(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	// Fail fast, before touching the lock or writing anything, when the
	// release isn't in a deployable state (e.g. already verified/deploying).
	// MarkDeploying re-checks this after prepareRun as defense in depth, but
	// checking here too avoids persisting an orphaned DeployLog/PipelineStep
	// set for a request that was never going to be allowed to proceed.
	if rel.Status != domain.ReleaseStatusDraft && rel.Status != domain.ReleaseStatusFailed {
		return nil, errors.New("cannot deploy release in status " + string(rel.Status))
	}

	lockKey := service.ReleaseLockKey(rel.ApplicationID, string(rel.Env), rel.InstanceName)
	token, err := a.lockSvc.AcquireKey(ctx, lockKey, a.lockTTL())
	if err != nil {
		return nil, err
	}

	log, blueprints, localPath, err := a.prepareRun(ctx, rel, target)
	if err != nil {
		a.lockSvc.ReleaseKey(ctx, lockKey, token)
		return nil, err
	}
	if err := a.releaseSvc.MarkDeploying(ctx, rel.ID, log.ID); err != nil {
		a.lockSvc.ReleaseKey(ctx, lockKey, token)
		return nil, err
	}

	go func() {
		defer a.lockSvc.ReleaseKey(context.Background(), lockKey, token)
		a.runAsync(rel.ID, target, log, blueprints, localPath)
	}()
	return log, nil
}

// Retry reruns a failed release's pipeline starting from fromIdx, reusing
// the same DeployLog row (previous successful steps are preserved).
func (a *ReleaseApplication) Retry(ctx context.Context, releaseID int64, fromIdx int) (*domain.DeployLog, error) {
	logger.L().Info("release: retry requested", zap.Int64("release_id", releaseID), zap.Int("from_idx", fromIdx))
	rel, target, log, blueprints, localPath, err := a.loadForResume(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	if log.Status != domain.DeployStatusFailed {
		return nil, errors.New("cannot retry run in status " + string(log.Status))
	}
	if fromIdx < 0 || fromIdx >= len(blueprints) {
		return nil, errors.New("from_step out of range")
	}

	lockKey := service.ReleaseLockKey(rel.ApplicationID, string(rel.Env), rel.InstanceName)
	token, err := a.lockSvc.AcquireKey(ctx, lockKey, a.lockTTL())
	if err != nil {
		return nil, err
	}
	if err := a.stepSvc.ResetFrom(ctx, log.ID, fromIdx); err != nil {
		a.lockSvc.ReleaseKey(ctx, lockKey, token)
		return nil, err
	}
	if err := a.releaseSvc.MarkDeploying(ctx, rel.ID, log.ID); err != nil {
		a.lockSvc.ReleaseKey(ctx, lockKey, token)
		return nil, err
	}

	go func() {
		defer a.lockSvc.ReleaseKey(context.Background(), lockKey, token)
		a.runAsync(rel.ID, target, log, blueprints, localPath)
	}()
	return a.logSvc.GetWithSteps(ctx, log.ID)
}

// Pause sets pause_requested=true on a running pipeline. The release stays
// "deploying" — Resume (or the eventual timeout) is what reaches a terminal
// state and syncs the release status.
func (a *ReleaseApplication) Pause(ctx context.Context, releaseID int64) error {
	logger.L().Info("release: pause requested", zap.Int64("release_id", releaseID))
	rel, err := a.releaseSvc.Get(ctx, releaseID)
	if err != nil {
		return err
	}
	log, err := a.logSvc.GetByID(ctx, rel.DeployLogID)
	if err != nil {
		return err
	}
	if log.Status != domain.DeployStatusRunning {
		return errors.New("cannot pause run in status " + string(log.Status))
	}
	return a.logSvc.SetPauseRequested(ctx, log.ID, true)
}

// Resume restarts a paused pipeline from the next pending step.
func (a *ReleaseApplication) Resume(ctx context.Context, releaseID int64) (*domain.DeployLog, error) {
	logger.L().Info("release: resume requested", zap.Int64("release_id", releaseID))
	rel, target, log, blueprints, localPath, err := a.loadForResume(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	if log.Status != domain.DeployStatusPaused {
		return nil, errors.New("cannot resume run in status " + string(log.Status))
	}

	lockKey := service.ReleaseLockKey(rel.ApplicationID, string(rel.Env), rel.InstanceName)
	token, err := a.lockSvc.AcquireKey(ctx, lockKey, a.lockTTL())
	if err != nil {
		return nil, err
	}

	go func() {
		defer a.lockSvc.ReleaseKey(context.Background(), lockKey, token)
		a.runAsync(rel.ID, target, log, blueprints, localPath)
	}()
	return a.logSvc.GetWithSteps(ctx, log.ID)
}

// Rollback creates a new release that redeploys targetReleaseID's commit
// (or the most recent verified release for the same app/env/instance when
// targetReleaseID is 0), triggers its deploy, and marks badReleaseID as
// rolling_back. Once the new release's deploy succeeds, badReleaseID is
// automatically marked rolled_back (see syncReleaseStatus).
//
// The new release's Deploy is triggered BEFORE badReleaseID is flagged
// rolling_back, deliberately: Deploy can fail before anything async starts
// (lock contention, a disabled target, etc), and if we flagged badReleaseID
// first we'd have no way to un-flag it — rolling_back accepts no further
// Rollback/Retry/Verify calls, so a failed Deploy would leave it stuck
// forever. Triggering Deploy first means a failure here leaves badReleaseID
// completely untouched and the whole call can just be retried.
func (a *ReleaseApplication) Rollback(ctx context.Context, badReleaseID, targetReleaseID int64) (*domain.ApplicationRelease, error) {
	logger.L().Info("release: rollback requested", zap.Int64("bad_release_id", badReleaseID), zap.Int64("target_release_id", targetReleaseID))
	bad, err := a.releaseSvc.Get(ctx, badReleaseID)
	if err != nil {
		return nil, err
	}
	if bad.Status == domain.ReleaseStatusRollingBack || bad.Status == domain.ReleaseStatusRolledBack {
		return nil, errors.New("release " + strconv.FormatInt(badReleaseID, 10) + " is already rolling back or rolled back")
	}
	target, err := a.releaseSvc.ResolveRollbackTarget(ctx, badReleaseID, targetReleaseID)
	if err != nil {
		return nil, err
	}
	newRel, err := a.releaseSvc.CreateRollbackDraft(ctx, bad, target)
	if err != nil {
		return nil, err
	}
	if _, err := a.Deploy(ctx, newRel.ID); err != nil {
		return nil, err
	}
	// Best-effort: the redeploy is already running at this point, so a
	// failure here (repository layer already logs it) just means
	// badReleaseID skips the rolling_back waypoint and later jumps straight
	// from its current status to rolled_back once the new release succeeds
	// (syncReleaseStatus) — not ideal bookkeeping, but never a stuck state.
	_ = a.releaseSvc.MarkRollingBack(ctx, badReleaseID)
	return newRel, nil
}

func (a *ReleaseApplication) loadTarget(ctx context.Context, releaseID int64) (*domain.ApplicationRelease, *domain.DeployTarget, error) {
	rel, err := a.releaseSvc.Get(ctx, releaseID)
	if err != nil {
		return nil, nil, err
	}
	app, err := a.appSvc.Get(ctx, rel.ApplicationID)
	if err != nil {
		return nil, nil, err
	}
	envTarget, err := a.appSvc.GetTargetForEnvInstance(ctx, rel.ApplicationID, rel.Env, rel.InstanceName)
	if err != nil {
		return nil, nil, err
	}
	target := &domain.DeployTarget{App: app, EnvTarget: envTarget, Branch: rel.Branch, CommitSHA: rel.CommitSHA}
	return rel, target, nil
}

// loadForResume reloads the release and its deploy_log, then rebuilds the
// pipeline blueprints needed to continue a run (retry/resume share this
// preamble).
//
// Unlike Deploy's loadTarget, this does NOT hand Builder the current live
// ApplicationEnvTarget: already-succeeded steps in this DeployLog (e.g.
// compose_up) physically ran against the machine/port/instance captured on
// the log row at the time of the original deploy. If the target's
// machine_id/port were edited since then, rebuilding against the live row
// would make the remaining steps target a different machine than the one
// the earlier steps actually used, while looking identical to a normal run.
// So the target's identity (ID/MachineID/Port/InstanceName) is frozen to
// what prepareRun captured on the log; only gateway route config (read from
// the live target) is allowed to be current, since gateway_route_sync is
// always still pending at retry/resume time.
func (a *ReleaseApplication) loadForResume(ctx context.Context, releaseID int64) (*domain.ApplicationRelease, *domain.DeployTarget, *domain.DeployLog, []pipeline.Blueprint, string, error) {
	rel, err := a.releaseSvc.Get(ctx, releaseID)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	log, err := a.logSvc.GetByID(ctx, rel.DeployLogID)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	app, err := a.appSvc.Get(ctx, rel.ApplicationID)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	liveTarget, err := a.appSvc.GetTargetForEnvInstance(ctx, rel.ApplicationID, rel.Env, rel.InstanceName)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	snapshot := *liveTarget
	snapshot.ID = log.DeployTargetID
	snapshot.MachineID = log.MachineID
	snapshot.Port = log.DeployPort
	snapshot.InstanceName = domain.NormalizeInstanceName(log.InstanceName)
	target := &domain.DeployTarget{App: app, EnvTarget: &snapshot, Branch: rel.Branch, CommitSHA: rel.CommitSHA}

	blueprints, localPath, err := a.builder.Build(ctx, target)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	return rel, target, log, blueprints, localPath, nil
}

// prepareRun is the shared setup for Deploy: build the pipeline blueprint,
// persist the log, and persist the initial step rows.
func (a *ReleaseApplication) prepareRun(ctx context.Context, rel *domain.ApplicationRelease, target *domain.DeployTarget) (*domain.DeployLog, []pipeline.Blueprint, string, error) {
	blueprints, localPath, err := a.builder.Build(ctx, target)
	if err != nil {
		return nil, nil, "", err
	}
	log := &domain.DeployLog{
		ApplicationID:  rel.ApplicationID,
		ReleaseID:      rel.ID,
		Env:            rel.Env,
		DeployTargetID: target.EnvTarget.ID,
		InstanceName:   target.EnvTarget.InstanceName,
		MachineID:      target.EnvTarget.MachineID,
		DeployPort:     target.EnvTarget.Port,
		Branch:         rel.Branch,
		TriggerSHA:     rel.CommitSHA,
		Status:         domain.DeployStatusPending,
	}
	if err := a.logSvc.Create(ctx, log); err != nil {
		return nil, nil, "", err
	}
	steps := pipeline.BlueprintToSteps(log.ID, blueprints)
	if err := a.stepSvc.CreateBatch(ctx, steps); err != nil {
		return nil, nil, "", err
	}
	log.Steps = steps
	return log, blueprints, localPath, nil
}

func (a *ReleaseApplication) runAsync(releaseID int64, target *domain.DeployTarget, log *domain.DeployLog, blueprints []pipeline.Blueprint, localPath string) {
	timeout := a.cfg.Deploy.DefaultTimeout.Duration
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	a.runner.Run(ctx, log, target, blueprints, localPath)
	a.syncReleaseStatus(context.WithoutCancel(ctx), releaseID, log)
}

// syncReleaseStatus folds the just-finished pipeline run's terminal status
// back into the release state machine. A paused run leaves the release in
// "deploying" — Resume (or a later Retry) is what eventually reaches a
// terminal state.
//
// This runs at the tail of a fire-and-forget goroutine, so there is no
// caller left to propagate an error to — but it does not log errors itself
// either: every call below (ApplicationReleaseService -> ApplicationReleaseRepository.UpdateStatus)
// already logs the underlying failure at the repository layer, and logging
// it again here would just duplicate that entry.
func (a *ReleaseApplication) syncReleaseStatus(ctx context.Context, releaseID int64, log *domain.DeployLog) {
	switch log.Status {
	case domain.DeployStatusSuccess:
		if err := a.releaseSvc.MarkDeploySucceeded(ctx, releaseID, log.TriggerSHA); err != nil {
			return
		}
		rel, err := a.releaseSvc.Get(ctx, releaseID)
		if err != nil {
			return
		}
		if rel.PreviousReleaseID > 0 {
			_ = a.releaseSvc.MarkRolledBack(ctx, rel.PreviousReleaseID)
		}
	case domain.DeployStatusFailed:
		_ = a.releaseSvc.MarkDeployFailed(ctx, releaseID)
	default:
		// paused or (unexpectedly) still running — nothing to sync yet.
	}
}

func (a *ReleaseApplication) lockTTL() time.Duration {
	ttl := a.cfg.Deploy.DefaultTimeout.Duration
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	return ttl + 30*time.Second
}
