package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/git"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
)

// ApplicationReleaseService owns the ApplicationRelease state machine:
//
//	draft -> deploying -> pending_verify -> verified
//	                \-> failed -> deploying (retry reuses the same deploy_log)
//	verified -> rolling_back -> rolled_back
//
// It reads Application/ApplicationEnvTarget/ApplicationGatewayRoute/
// ApplicationEnvironment directly via their repositories (not through their
// services) purely to build point-in-time snapshots at release-creation
// time — this is plain data assembly, not business-logic delegation, so it
// does not violate the "no service-to-service calls" rule. Orchestrating an
// actual pipeline run belongs to the application layer (ReleaseApplication).
type ApplicationReleaseService struct {
	releases *repository.ApplicationReleaseRepository
	apps     *repository.ApplicationRepository
	targets  *repository.ApplicationTargetRepository
	routes   *repository.ApplicationGatewayRouteRepository
	envs     *repository.ApplicationEnvironmentRepository
}

func NewApplicationReleaseService(
	releases *repository.ApplicationReleaseRepository,
	apps *repository.ApplicationRepository,
	targets *repository.ApplicationTargetRepository,
	routes *repository.ApplicationGatewayRouteRepository,
	envs *repository.ApplicationEnvironmentRepository,
) *ApplicationReleaseService {
	return &ApplicationReleaseService{releases: releases, apps: apps, targets: targets, routes: routes, envs: envs}
}

type CreateReleaseRequest struct {
	Env          domain.Environment `json:"env"           binding:"required"`
	InstanceName string             `json:"instance_name"`
	Branch       string             `json:"branch"         binding:"required"`
	CommitSHA    string             `json:"commit_sha"`
	Operator     string             `json:"operator"`
}

func (s *ApplicationReleaseService) Create(ctx context.Context, appID int64, req CreateReleaseRequest) (*domain.ApplicationRelease, error) {
	env := domain.DefaultEnvironment(req.Env)
	if !env.Valid() {
		return nil, errors.New(fmt.Sprintf("invalid env %q", env))
	}
	instanceName := domain.NormalizeInstanceName(req.InstanceName)

	app, err := s.apps.GetByID(ctx, appID)
	if err != nil {
		return nil, err
	}
	target, err := s.targets.GetByApplicationEnvInstance(ctx, appID, env, instanceName)
	if err != nil {
		return nil, err
	}
	if !target.Enabled {
		return nil, errors.New(fmt.Sprintf("%s/%s target is disabled", env, instanceName))
	}

	commitSHA, err := git.NormalizeCommitSHA(req.CommitSHA)
	if err != nil {
		return nil, err
	}

	rel, err := s.buildDraft(ctx, app, target, req.Branch, commitSHA, req.Operator, 0)
	if err != nil {
		return nil, err
	}
	if err := s.releases.Create(ctx, rel); err != nil {
		return nil, err
	}
	logStruct("release created", rel)
	return rel, nil
}

// CreateBatchChild persists a draft release for one instance of an env-wide
// deploy batch, tagged with envDeploymentID so the batch can later roll its
// children up. Unlike Create it takes the already-loaded app/target (the batch
// orchestrator lists every enabled target once up front) and does not re-check
// Enabled — the caller only ever passes enabled targets.
func (s *ApplicationReleaseService) CreateBatchChild(ctx context.Context, app *domain.Application, target *domain.ApplicationEnvTarget, branch, commitSHA, operator string, envDeploymentID int64) (*domain.ApplicationRelease, error) {
	rel, err := s.buildDraft(ctx, app, target, branch, commitSHA, operator, envDeploymentID)
	if err != nil {
		return nil, err
	}
	if err := s.releases.Create(ctx, rel); err != nil {
		return nil, err
	}
	logStruct("batch child release created", rel)
	return rel, nil
}

// buildDraft assembles a new, unpersisted draft release for (app, target),
// snapshotting the app's current deploy config / env-level config / gateway
// routes so later inspection of a past release reflects what was configured
// at the time, not whatever the application looks like today.
func (s *ApplicationReleaseService) buildDraft(ctx context.Context, app *domain.Application, target *domain.ApplicationEnvTarget, branch, commitSHA, operator string, envDeploymentID int64) (*domain.ApplicationRelease, error) {
	envRow, err := s.envs.GetByApplicationEnv(ctx, app.ID, target.Env)
	if err != nil {
		return nil, err
	}
	envConfigSnapshot := ""
	if envRow != nil {
		envConfigSnapshot, err = sonicMarshal(envRow)
		if err != nil {
			return nil, err
		}
	}
	routes, err := s.routes.ListByApplicationEnv(ctx, app.ID, target.Env)
	if err != nil {
		return nil, err
	}
	routeSnapshot, err := sonicMarshal(routes)
	if err != nil {
		return nil, err
	}

	return &domain.ApplicationRelease{
		ApplicationID:        app.ID,
		Env:                  target.Env,
		InstanceName:         target.InstanceName,
		MachineID:            target.MachineID,
		EnvDeploymentID:      envDeploymentID,
		Branch:               branch,
		CommitSHA:            commitSHA,
		Status:               domain.ReleaseStatusDraft,
		DeployConfigSnapshot: app.DeployConfig,
		EnvConfigSnapshot:    envConfigSnapshot,
		RouteSnapshot:        routeSnapshot,
		Operator:             operator,
	}, nil
}

func (s *ApplicationReleaseService) Get(ctx context.Context, id int64) (*domain.ApplicationRelease, error) {
	return s.releases.GetByID(ctx, id)
}

// GetLatestVerified returns the most recent verified release for one
// (app, env, instance), or nil when the instance has never had a successful
// deploy. Used to resolve an instance's current running branch (e.g. for the
// decommission compose project name).
func (s *ApplicationReleaseService) GetLatestVerified(ctx context.Context, appID int64, env domain.Environment, instanceName string) (*domain.ApplicationRelease, error) {
	return s.releases.GetLatestVerified(ctx, appID, env, domain.NormalizeInstanceName(instanceName))
}

type ListReleasesFilter = repository.ListReleasesFilter

func (s *ApplicationReleaseService) List(ctx context.Context, f ListReleasesFilter) ([]*domain.ApplicationRelease, error) {
	return s.releases.List(ctx, f)
}

// ResolveRollbackTarget returns the release a rollback should redeploy: the
// explicitly requested targetReleaseID if given (must be verified and belong
// to the same app/env/instance as badReleaseID), otherwise the most recent
// verified release created before badReleaseID.
//
// The automatic target deliberately looks *before* the bad release rather than
// simply taking the instance's latest verified release: a problem is often only
// noticed after someone already clicked verify, and "latest verified" would
// then resolve to the bad release itself and refuse to roll anything back.
func (s *ApplicationReleaseService) ResolveRollbackTarget(ctx context.Context, badReleaseID, targetReleaseID int64) (*domain.ApplicationRelease, error) {
	bad, err := s.releases.GetByID(ctx, badReleaseID)
	if err != nil {
		return nil, err
	}
	if targetReleaseID > 0 {
		target, err := s.releases.GetByID(ctx, targetReleaseID)
		if err != nil {
			return nil, err
		}
		if target.ApplicationID != bad.ApplicationID || target.Env != bad.Env || target.InstanceName != bad.InstanceName {
			return nil, errors.New("rollback target release does not belong to the same application/env/instance")
		}
		if target.Status != domain.ReleaseStatusVerified {
			return nil, errors.New(fmt.Sprintf("rollback target release is %s, not verified", target.Status))
		}
		return target, nil
	}
	prev, err := s.releases.GetPreviousVerified(ctx, bad.ApplicationID, bad.Env, bad.InstanceName, bad.ID)
	if err != nil {
		return nil, err
	}
	if prev == nil {
		return nil, errors.New("no earlier verified release to roll back to")
	}
	return prev, nil
}

// CreateRollbackDraft builds (and persists as draft) a new release that
// redeploys target's commit, linked back to the release it supersedes.
// envDeploymentID groups it under an env-wide rollback batch, or is 0 for a
// single-instance rollback. operator is whoever asked for the rollback, falling
// back to the superseded release's operator when the caller has no identity —
// attributing a rollback to whoever ran the bad deploy would misread deploy
// history exactly when someone needs to trust it.
//
// Only the commit is rolled back — the new release snapshots the application's
// *current* deploy config, environment variables and gateway routes, exactly
// like any other deploy. Rolling configuration back alongside code would mean
// silently reverting changes an operator may have made deliberately since, and
// there is no way to tell the two apart from here; an operator who wants the
// old configuration back edits it and deploys again.
func (s *ApplicationReleaseService) CreateRollbackDraft(ctx context.Context, bad, target *domain.ApplicationRelease, envDeploymentID int64, operator string) (*domain.ApplicationRelease, error) {
	app, err := s.apps.GetByID(ctx, bad.ApplicationID)
	if err != nil {
		return nil, err
	}
	envTarget, err := s.targets.GetByApplicationEnvInstance(ctx, bad.ApplicationID, bad.Env, bad.InstanceName)
	if err != nil {
		return nil, err
	}
	if !envTarget.Enabled {
		return nil, errors.New(fmt.Sprintf("%s/%s target is disabled", bad.Env, bad.InstanceName))
	}
	if operator == "" {
		operator = bad.Operator
	}
	rel, err := s.buildDraft(ctx, app, envTarget, target.Branch, target.CommitSHA, operator, envDeploymentID)
	if err != nil {
		return nil, err
	}
	rel.PreviousReleaseID = bad.ID
	rel.PreviousCommitSHA = bad.CommitSHA
	if err := s.releases.Create(ctx, rel); err != nil {
		return nil, err
	}
	logStruct("rollback release created", rel)
	return rel, nil
}

// EnvRollbackPair is one instance's rollback decision: the release being
// superseded and the earlier verified release its commit will be redeployed
// from.
type EnvRollbackPair struct {
	Bad    *domain.ApplicationRelease
	Target *domain.ApplicationRelease
}

// PlanEnvRollback resolves the rollback target for every child release of an
// env-wide deploy, and is all-or-nothing: if any instance that needs rolling
// back has no earlier verified release, the whole plan is rejected rather than
// returning a partial one. Half-rolling-back an environment would leave two
// different versions of the application behind the same gateway route, which
// is a worse state than the bad deploy the operator is trying to undo.
//
// Children that never changed what is running on their instance are skipped
// rather than failing the plan: a draft or failed child never got as far as
// replacing anything, and an already rolled_back one has been dealt with. A
// child still deploying does fail the plan — its outcome is not yet known, so
// neither rolling it back nor ignoring it is defensible.
//
// A rolling_back child is planned again rather than refused. rolling_back only
// records that a replacement was once started, and nothing ever clears it if
// that replacement then failed — so refusing here would make one failed
// rollback permanently block every future rollback of the whole environment,
// with the instance still running the bad code. Replaying it is safe: if the
// earlier replacement is genuinely still in flight, the per-instance deploy
// lock rejects the new one (RollbackEnv records that child as failed and the
// batch reports it), which is recoverable, unlike a dead end.
func (s *ApplicationReleaseService) PlanEnvRollback(ctx context.Context, children []*domain.ApplicationRelease) ([]EnvRollbackPair, error) {
	pairs := make([]EnvRollbackPair, 0, len(children))
	for _, child := range children {
		switch child.Status {
		case domain.ReleaseStatusDraft, domain.ReleaseStatusFailed, domain.ReleaseStatusRolledBack:
			continue
		case domain.ReleaseStatusDeploying:
			return nil, errors.New(fmt.Sprintf("instance %s is still deploying; wait for it to finish before rolling back", child.InstanceName))
		}
		target, err := s.ResolveRollbackTarget(ctx, child.ID, 0)
		if err != nil {
			return nil, errors.New(fmt.Sprintf("instance %s: %s", child.InstanceName, err.Error()))
		}
		pairs = append(pairs, EnvRollbackPair{Bad: child, Target: target})
	}
	if len(pairs) == 0 {
		return nil, errors.New("no instance in this deploy has anything to roll back")
	}
	return pairs, nil
}

func (s *ApplicationReleaseService) Verify(ctx context.Context, id int64) (*domain.ApplicationRelease, error) {
	rel, err := s.releases.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if rel.Status != domain.ReleaseStatusPendingVerify {
		return nil, errors.New(fmt.Sprintf("cannot verify release in status %s", rel.Status))
	}
	now := time.Now().UTC()
	if err := s.releases.UpdateStatus(ctx, id, domain.ReleaseStatusVerified, map[string]interface{}{"verified_at": &now}); err != nil {
		return nil, err
	}
	rel.Status = domain.ReleaseStatusVerified
	rel.VerifiedAt = &now
	logger.L().Info("release verified", zap.Int64("id", id))
	return rel, nil
}

// MarkDeploying transitions draft/failed -> deploying and records the
// deploy_log driving this attempt.
func (s *ApplicationReleaseService) MarkDeploying(ctx context.Context, id, deployLogID int64) error {
	rel, err := s.releases.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if rel.Status != domain.ReleaseStatusDraft && rel.Status != domain.ReleaseStatusFailed {
		return errors.New(fmt.Sprintf("cannot deploy release in status %s", rel.Status))
	}
	return s.releases.UpdateStatus(ctx, id, domain.ReleaseStatusDeploying, map[string]interface{}{"deploy_log_id": deployLogID})
}

// MarkDeploySucceeded transitions deploying -> pending_verify. resolvedSHA is
// the commit git_pull actually checked out (equal to the pinned commit_sha
// when one was given, otherwise the resolved branch HEAD).
func (s *ApplicationReleaseService) MarkDeploySucceeded(ctx context.Context, id int64, resolvedSHA string) error {
	now := time.Now().UTC()
	extra := map[string]interface{}{"deployed_at": &now}
	if resolvedSHA != "" {
		extra["commit_sha"] = resolvedSHA
	}
	return s.releases.UpdateStatus(ctx, id, domain.ReleaseStatusPendingVerify, extra)
}

func (s *ApplicationReleaseService) MarkDeployFailed(ctx context.Context, id int64) error {
	return s.releases.UpdateStatus(ctx, id, domain.ReleaseStatusFailed, nil)
}

func (s *ApplicationReleaseService) MarkRollingBack(ctx context.Context, id int64) error {
	return s.releases.UpdateStatus(ctx, id, domain.ReleaseStatusRollingBack, nil)
}

func (s *ApplicationReleaseService) MarkRolledBack(ctx context.Context, id int64) error {
	return s.releases.UpdateStatus(ctx, id, domain.ReleaseStatusRolledBack, nil)
}
