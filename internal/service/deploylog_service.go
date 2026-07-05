package service

import (
	"context"

	"github.com/agenda-v2/internal/domain"
	"github.com/agenda-v2/internal/repository"
)

// ListLogsFilter is re-exported from repository so handlers don't depend on repository.
type ListLogsFilter = repository.ListLogsFilter

type DeployLogService struct {
	logs  *repository.DeployLogRepository
	steps *repository.PipelineStepRepository
}

func NewDeployLogService(logs *repository.DeployLogRepository, steps *repository.PipelineStepRepository) *DeployLogService {
	return &DeployLogService{logs: logs, steps: steps}
}

func (s *DeployLogService) Create(ctx context.Context, l *domain.DeployLog) error {
	if err := s.logs.Create(ctx, l); err != nil {
		return err
	}
	logStruct("deploy log created", l)
	return nil
}

func (s *DeployLogService) Finish(ctx context.Context, l *domain.DeployLog) error {
	return s.logs.Finish(ctx, l)
}

func (s *DeployLogService) GetByID(ctx context.Context, id int64) (*domain.DeployLog, error) {
	return s.logs.GetByID(ctx, id)
}

// GetWithSteps fetches the run and attaches its ordered pipeline steps.
func (s *DeployLogService) GetWithSteps(ctx context.Context, id int64) (*domain.DeployLog, error) {
	log, err := s.logs.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	steps, err := s.steps.ListByLog(ctx, id)
	if err != nil {
		return nil, err
	}
	log.Steps = steps
	return log, nil
}

func (s *DeployLogService) SetPauseRequested(ctx context.Context, id int64, paused bool) error {
	return s.logs.SetPauseRequested(ctx, id, paused)
}

func (s *DeployLogService) MarkRunning(ctx context.Context, id int64) error {
	return s.logs.MarkRunning(ctx, id)
}

func (s *DeployLogService) MarkPaused(ctx context.Context, id int64, triggerSHA string) error {
	return s.logs.MarkPaused(ctx, id, triggerSHA)
}

func (s *DeployLogService) List(ctx context.Context, f ListLogsFilter) ([]*domain.DeployLog, error) {
	return s.logs.List(ctx, f)
}
