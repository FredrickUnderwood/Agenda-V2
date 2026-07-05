package repository

import (
	"context"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/agenda-v2/internal/domain"
	"github.com/agenda-v2/internal/logger"
)

type PipelineStepRepository struct{ db *gorm.DB }

func NewPipelineStepRepository(db *gorm.DB) *PipelineStepRepository {
	return &PipelineStepRepository{db: db}
}

// CreateBatch persists the full step list for a run in order. Assumes each
// entry already has DeployLogID / Idx / Name / StepType populated.
func (r *PipelineStepRepository) CreateBatch(ctx context.Context, steps []*domain.PipelineStep) error {
	if len(steps) == 0 {
		return nil
	}
	if err := r.db.WithContext(ctx).Create(&steps).Error; err != nil {
		logger.L().Error("failed to create pipeline steps", zap.Int64("deploy_log_id", steps[0].DeployLogID), zap.Error(err))
		return err
	}
	return nil
}

// ListByLog returns steps for a run ordered by idx ascending.
func (r *PipelineStepRepository) ListByLog(ctx context.Context, deployLogID int64) ([]*domain.PipelineStep, error) {
	var steps []*domain.PipelineStep
	if err := r.db.WithContext(ctx).
		Where("deploy_log_id = ?", deployLogID).
		Order("idx ASC").
		Find(&steps).Error; err != nil {
		logger.L().Error("failed to list pipeline steps", zap.Int64("deploy_log_id", deployLogID), zap.Error(err))
		return nil, err
	}
	return steps, nil
}

// Update persists the mutable fields of a single step. Uses Updates so zero
// values for FinishedAt (nil) still write through.
func (r *PipelineStepRepository) Update(ctx context.Context, s *domain.PipelineStep) error {
	if err := r.db.WithContext(ctx).
		Model(&domain.PipelineStep{}).
		Where("id = ?", s.ID).
		Updates(map[string]interface{}{
			"status":      s.Status,
			"output":      s.Output,
			"error_msg":   s.ErrorMsg,
			"started_at":  s.StartedAt,
			"finished_at": s.FinishedAt,
			"duration_ms": s.DurationMs,
			"attempt":     s.Attempt,
		}).Error; err != nil {
		logger.L().Error("failed to update pipeline step", zap.Int64("id", s.ID), zap.Error(err))
		return err
	}
	return nil
}

// ResetFrom clears output/error/timestamps for steps at idx >= fromIdx on the
// given run and flips them back to pending. The step at fromIdx has its
// attempt counter incremented.
func (r *PipelineStepRepository) ResetFrom(ctx context.Context, deployLogID int64, fromIdx int) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.PipelineStep{}).
			Where("deploy_log_id = ? AND idx >= ?", deployLogID, fromIdx).
			Updates(map[string]interface{}{
				"status":      domain.StepStatusPending,
				"output":      "",
				"error_msg":   "",
				"started_at":  nil,
				"finished_at": nil,
				"duration_ms": 0,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&domain.PipelineStep{}).
			Where("deploy_log_id = ? AND idx = ?", deployLogID, fromIdx).
			UpdateColumn("attempt", gorm.Expr("attempt + 1")).Error
	})
	if err != nil {
		logger.L().Error("failed to reset pipeline steps",
			zap.Int64("deploy_log_id", deployLogID), zap.Int("from_idx", fromIdx), zap.Error(err))
		return err
	}
	return nil
}

// DeleteByLog removes all steps for a run. There is no DB-level cascade (no
// FK by design), so callers delete explicitly when a DeployLog is removed.
func (r *PipelineStepRepository) DeleteByLog(ctx context.Context, deployLogID int64) error {
	if err := r.db.WithContext(ctx).
		Where("deploy_log_id = ?", deployLogID).
		Delete(&domain.PipelineStep{}).Error; err != nil {
		logger.L().Error("failed to delete pipeline steps", zap.Int64("deploy_log_id", deployLogID), zap.Error(err))
		return err
	}
	return nil
}
