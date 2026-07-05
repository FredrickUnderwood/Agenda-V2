package service

import (
	"context"

	"github.com/agenda-v2/internal/domain"
	"github.com/agenda-v2/internal/repository"
)

type PipelineStepService struct {
	steps *repository.PipelineStepRepository
}

func NewPipelineStepService(steps *repository.PipelineStepRepository) *PipelineStepService {
	return &PipelineStepService{steps: steps}
}

func (s *PipelineStepService) CreateBatch(ctx context.Context, steps []*domain.PipelineStep) error {
	return s.steps.CreateBatch(ctx, steps)
}

func (s *PipelineStepService) ListByLog(ctx context.Context, deployLogID int64) ([]*domain.PipelineStep, error) {
	return s.steps.ListByLog(ctx, deployLogID)
}

func (s *PipelineStepService) Update(ctx context.Context, step *domain.PipelineStep) error {
	return s.steps.Update(ctx, step)
}

func (s *PipelineStepService) ResetFrom(ctx context.Context, deployLogID int64, fromIdx int) error {
	return s.steps.ResetFrom(ctx, deployLogID, fromIdx)
}

func (s *PipelineStepService) DeleteByLog(ctx context.Context, deployLogID int64) error {
	return s.steps.DeleteByLog(ctx, deployLogID)
}
