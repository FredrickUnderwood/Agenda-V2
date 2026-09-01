package service

import (
	"context"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
)

// ListEnvDeploymentsFilter is re-exported from repository so handlers don't
// depend on repository directly.
type ListEnvDeploymentsFilter = repository.ListEnvDeploymentsFilter

// EnvDeploymentService owns the env-wide deploy batch record and derives its
// aggregate status from the child per-instance releases. It never authors a
// child's status — the ApplicationRelease state machine does — it only rolls
// the children up into the batch summary (Reconcile).
type EnvDeploymentService struct {
	batches  *repository.EnvDeploymentRepository
	releases *repository.ApplicationReleaseRepository
}

func NewEnvDeploymentService(
	batches *repository.EnvDeploymentRepository,
	releases *repository.ApplicationReleaseRepository,
) *EnvDeploymentService {
	return &EnvDeploymentService{batches: batches, releases: releases}
}

func (s *EnvDeploymentService) Create(ctx context.Context, d *domain.EnvDeployment) error {
	if err := s.batches.Create(ctx, d); err != nil {
		return err
	}
	logStruct("env deployment created", d)
	return nil
}

// Get returns the batch with its child releases attached.
func (s *EnvDeploymentService) Get(ctx context.Context, id int64) (*domain.EnvDeployment, error) {
	batch, err := s.batches.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	children, err := s.releases.ListByEnvDeployment(ctx, id)
	if err != nil {
		return nil, err
	}
	batch.Releases = children
	return batch, nil
}

func (s *EnvDeploymentService) List(ctx context.Context, f ListEnvDeploymentsFilter) ([]*domain.EnvDeployment, error) {
	return s.batches.List(ctx, f)
}

// GetUnfinishedRollbackOf returns a not-yet-finished rollback batch superseding
// id, or nil when there is none. Callers use it to reject a duplicate rollback
// request before it creates a second batch.
func (s *EnvDeploymentService) GetUnfinishedRollbackOf(ctx context.Context, id int64) (*domain.EnvDeployment, error) {
	return s.batches.GetUnfinishedRollbackOf(ctx, id)
}

// Reconcile recomputes a batch's aggregate status and counts from the current
// state of its child releases, then persists the snapshot. It is idempotent
// and race-free under parallel fan-out: because every child calls Reconcile
// after persisting its own terminal status, the last child to finish always
// observes every child in a terminal state and writes the final result.
//
// A child counts as succeeded once its deploy pipeline reached the release
// (pending_verify or verified); as failed when the release is failed; and as
// still in flight while it is draft/deploying. rolling_back/rolled_back are
// treated as terminal-success for batch purposes — the instance did deploy;
// any later rollback is a separate, per-instance concern.
func (s *EnvDeploymentService) Reconcile(ctx context.Context, id int64) (*domain.EnvDeployment, error) {
	batch, err := s.batches.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	children, err := s.releases.ListByEnvDeployment(ctx, id)
	if err != nil {
		return nil, err
	}

	var success, failed, inFlight int
	for _, rel := range children {
		switch rel.Status {
		case domain.ReleaseStatusPendingVerify, domain.ReleaseStatusVerified,
			domain.ReleaseStatusRollingBack, domain.ReleaseStatusRolledBack:
			success++
		case domain.ReleaseStatusFailed:
			failed++
		default: // draft, deploying
			inFlight++
		}
	}

	status := domain.EnvDeploymentStatusRunning
	var finishedAt *time.Time
	if inFlight == 0 && len(children) > 0 {
		now := time.Now().UTC()
		finishedAt = &now
		switch {
		case failed == 0:
			status = domain.EnvDeploymentStatusSuccess
		case success == 0:
			status = domain.EnvDeploymentStatusFailed
		default:
			status = domain.EnvDeploymentStatusPartial
		}
	}

	if err := s.batches.UpdateAggregate(ctx, id, status, len(children), success, failed, finishedAt); err != nil {
		return nil, err
	}
	batch.Status = status
	batch.TotalCount = len(children)
	batch.SuccessCount = success
	batch.FailedCount = failed
	batch.FinishedAt = finishedAt
	batch.Releases = children
	return batch, nil
}
