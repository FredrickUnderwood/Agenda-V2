package application

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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
	// envDeploySvc owns the env-wide deploy batch record. Nil-safe: a build
	// wired without it still does single-instance deploys; only DeployEnv and
	// the batch reconcile hook need it.
	envDeploySvc *service.EnvDeploymentService
	// alerts fires an ops notification (and inbox record) when a deploy run
	// ends in failure. Optional/nil-safe: a build without alerting configured
	// still deploys; it just doesn't emit the failure notice.
	alerts *service.AlertService
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
	envDeploySvc *service.EnvDeploymentService,
	alerts *service.AlertService,
) *ReleaseApplication {
	return &ReleaseApplication{
		cfg: cfg, builder: builder, runner: runner,
		logSvc: logSvc, stepSvc: stepSvc, appSvc: appSvc,
		releaseSvc: releaseSvc, lockSvc: lockSvc, envDeploySvc: envDeploySvc, alerts: alerts,
	}
}

// DeployEnv fans a single "deploy this branch to (app, env)" intent out to one
// release+deploy per instance, all recorded under one EnvDeployment batch. When
// instanceName is empty the batch targets every enabled instance of the env;
// when set it targets just that one instance (a batch of one) — the single
// "Deploy" entry point covers both. Each child acquires its own per-instance
// lock and runs independently (in parallel), so one instance's lock contention
// or build failure never blocks the others. Returns the batch immediately with
// its freshly created child releases; their pipelines finish asynchronously,
// each reconciling the batch's aggregate status as it terminates.
func (a *ReleaseApplication) DeployEnv(ctx context.Context, appID int64, env domain.Environment, instanceName, branch, commitSHA, operator string) (*domain.EnvDeployment, error) {
	env = domain.DefaultEnvironment(env)
	if !env.Valid() {
		return nil, errors.New("invalid env " + string(env))
	}
	if strings.TrimSpace(branch) == "" {
		branch = domain.DefaultBranch
	}
	app, err := a.appSvc.Get(ctx, appID)
	if err != nil {
		return nil, err
	}
	targets, err := a.appSvc.ListTargetsByApplication(ctx, appID, env)
	if err != nil {
		return nil, err
	}
	// An empty instanceName means "every enabled instance of the env"; a
	// specific one narrows the batch to that single instance.
	wantInstance := strings.TrimSpace(instanceName)
	if wantInstance != "" {
		wantInstance = domain.NormalizeInstanceName(wantInstance)
	}
	enabled := make([]*domain.ApplicationEnvTarget, 0, len(targets))
	for _, t := range targets {
		if eligibleForEnvDeploy(t, wantInstance) {
			enabled = append(enabled, t)
		}
	}
	if len(enabled) == 0 {
		if wantInstance != "" {
			return nil, errors.New("instance " + wantInstance + " is not an enabled instance of " + string(env))
		}
		return nil, errors.New("no enabled instances to deploy in " + string(env))
	}

	batch := &domain.EnvDeployment{
		ApplicationID: appID,
		Env:           env,
		Branch:        branch,
		CommitSHA:     commitSHA,
		Operator:      operator,
		Status:        domain.EnvDeploymentStatusRunning,
		TotalCount:    len(enabled),
	}
	if err := a.envDeploySvc.Create(ctx, batch); err != nil {
		return nil, err
	}
	logger.L().Info("release: env deploy requested",
		zap.Int64("application_id", appID), zap.String("env", string(env)),
		zap.String("branch", branch), zap.Int("instances", len(enabled)), zap.Int64("batch_id", batch.ID))

	for _, t := range enabled {
		rel, err := a.releaseSvc.CreateBatchChild(ctx, app, t, branch, commitSHA, operator, batch.ID)
		if err != nil {
			// A child that can't even be drafted never becomes a row Reconcile
			// can see; Reconcile derives total/counts from the children that do
			// exist, so simply skipping keeps the aggregate honest.
			logger.L().Error("release: env deploy child draft failed",
				zap.Int64("batch_id", batch.ID), zap.String("instance", t.InstanceName), zap.Error(err))
			continue
		}
		if _, err := a.Deploy(ctx, rel.ID); err != nil {
			// Deploy can fail before anything async starts (lock contention, a
			// target disabled between listing and here). Flip the child to
			// failed so Reconcile counts it as a terminal failure rather than
			// waiting forever on an in-flight release that will never run.
			logger.L().Error("release: env deploy child start failed",
				zap.Int64("batch_id", batch.ID), zap.Int64("release_id", rel.ID), zap.Error(err))
			_ = a.releaseSvc.MarkDeployFailed(ctx, rel.ID)
		}
	}

	// Reconcile once synchronously so a batch whose every child failed to start
	// (or was dropped) already reflects its terminal status on return, without
	// waiting for an async callback that will never come.
	if _, err := a.envDeploySvc.Reconcile(ctx, batch.ID); err != nil {
		logger.L().Error("release: env deploy initial reconcile failed", zap.Int64("batch_id", batch.ID), zap.Error(err))
	}
	return a.envDeploySvc.Get(ctx, batch.ID)
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
	a.syncReleaseStatus(context.WithoutCancel(ctx), releaseID, target, log)
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
func (a *ReleaseApplication) syncReleaseStatus(ctx context.Context, releaseID int64, target *domain.DeployTarget, log *domain.DeployLog) {
	switch log.Status {
	case domain.DeployStatusSuccess:
		if err := a.releaseSvc.MarkDeploySucceeded(ctx, releaseID, log.TriggerSHA); err != nil {
			return
		}
		// A successful deploy is the sole "bring back online" path: if this
		// instance was decommissioned (DesiredState=stopped), the running
		// containers now contradict that intent, so flip it back to running.
		// This is why there is no separate recommission action — deploying a
		// stopped instance restarts it and clears the stopped state in one step.
		// Best-effort: the release already succeeded; a failure here only leaves
		// a stale flag the next deploy will correct (repo layer logs it).
		if target != nil && target.EnvTarget != nil && target.EnvTarget.Stopped() {
			_ = a.appSvc.SetInstanceDesiredState(ctx, target.EnvTarget.ID, domain.RuntimeStateRunning)
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
		a.alertDeployFailed(ctx, target, log)
	default:
		// paused or (unexpectedly) still running — nothing to sync yet.
		return
	}

	// If this release belongs to an env-wide deploy batch, roll its now-updated
	// status up into the batch aggregate. Runs after the release's own status
	// was persisted above, so the last child to finish observes every sibling
	// terminal and writes the batch's final status (see EnvDeploymentService.
	// Reconcile). Best-effort: the repository layer already logs failures.
	if a.envDeploySvc != nil {
		if rel, err := a.releaseSvc.Get(ctx, releaseID); err == nil && rel.EnvDeploymentID > 0 {
			_, _ = a.envDeploySvc.Reconcile(ctx, rel.EnvDeploymentID)
		}
	}
}

// alertDeployFailed emits an ops notification (chat channels + in-app inbox)
// for a failed deploy run. Nil-safe and best-effort: it must never affect the
// deploy outcome, and it bounds its own send so a dead webhook can't wedge the
// tail of the run goroutine.
func (a *ReleaseApplication) alertDeployFailed(ctx context.Context, target *domain.DeployTarget, log *domain.DeployLog) {
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
	fmt.Fprintf(&b, "Deploy failed for %s (%s/%s).\n", appName, env, instance)
	if target.Branch != "" {
		fmt.Fprintf(&b, "Branch: %s\n", target.Branch)
	}
	if log != nil {
		if log.TriggerSHA != "" {
			fmt.Fprintf(&b, "Commit: %s\n", log.TriggerSHA)
		}
		fmt.Fprintf(&b, "Deploy log: #%d", log.ID)
	}

	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	a.alerts.SendToAll(sendCtx, alertsdk.Message{
		Title:   fmt.Sprintf("Deploy failed: %s (%s)", appName, env),
		Content: b.String(),
		Level:   alertsdk.LevelWarning,
	})
}

func (a *ReleaseApplication) lockTTL() time.Duration {
	ttl := a.cfg.Deploy.DefaultTimeout.Duration
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	return ttl + 30*time.Second
}

// eligibleForEnvDeploy decides whether a target is included in a DeployEnv
// fan-out. wantInstance is the already-normalized instance filter ("" = whole
// env). A disabled target is never deployed. When an instance is named, the
// fan-out targets exactly it — even if stopped, because an explicit deploy is
// how a decommissioned instance is restarted. When no instance is named, stopped
// (decommissioned) instances are skipped so a whole-env deploy never silently
// resurrects one an operator stopped — decommission is sticky, mirroring how a
// disabled instance is skipped.
func eligibleForEnvDeploy(t *domain.ApplicationEnvTarget, wantInstance string) bool {
	if !t.Enabled {
		return false
	}
	if wantInstance != "" {
		return t.InstanceName == wantInstance
	}
	return !t.Stopped()
}
