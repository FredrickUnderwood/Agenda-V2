package repository

import (
	"context"
	"errors"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

type ApplicationInstanceHealthRepository struct{ db *gorm.DB }

func NewApplicationInstanceHealthRepository(db *gorm.DB) *ApplicationInstanceHealthRepository {
	return &ApplicationInstanceHealthRepository{db: db}
}

func (r *ApplicationInstanceHealthRepository) GetByTargetID(ctx context.Context, targetID int64) (*domain.ApplicationInstanceHealth, error) {
	var health domain.ApplicationInstanceHealth
	if err := r.db.WithContext(ctx).
		Where("target_id = ?", targetID).
		First(&health).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		logger.L().Error("failed to get application instance health", zap.Int64("target_id", targetID), zap.Error(err))
		return nil, err
	}
	return &health, nil
}

func (r *ApplicationInstanceHealthRepository) ListByTargetIDs(ctx context.Context, targetIDs []int64) ([]*domain.ApplicationInstanceHealth, error) {
	if len(targetIDs) == 0 {
		return nil, nil
	}
	var health []*domain.ApplicationInstanceHealth
	if err := r.db.WithContext(ctx).
		Where("target_id IN ?", targetIDs).
		Find(&health).Error; err != nil {
		logger.L().Error("failed to list application instance health", zap.Error(err))
		return nil, err
	}
	return health, nil
}

func (r *ApplicationInstanceHealthRepository) Upsert(ctx context.Context, health *domain.ApplicationInstanceHealth) error {
	var existing domain.ApplicationInstanceHealth
	err := r.db.WithContext(ctx).
		Where("target_id = ?", health.TargetID).
		First(&existing).Error
	if err == nil {
		health.ID = existing.ID
		if err := r.db.WithContext(ctx).
			Model(&domain.ApplicationInstanceHealth{}).
			Where("id = ?", existing.ID).
			Updates(map[string]interface{}{
				"application_id":        health.ApplicationID,
				"env":                   health.Env,
				"instance_name":         health.InstanceName,
				"status":                health.Status,
				"checked_at":            health.CheckedAt,
				"http_status":           health.HTTPStatus,
				"latency_ms":            health.LatencyMS,
				"error_msg":             health.ErrorMsg,
				"consecutive_successes": health.ConsecutiveSuccesses,
				"consecutive_failures":  health.ConsecutiveFailures,
				"last_success_at":       health.LastSuccessAt,
				"last_failure_at":       health.LastFailureAt,
			}).Error; err != nil {
			logger.L().Error("failed to update application instance health", zap.Int64("target_id", health.TargetID), zap.Error(err))
			return err
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.L().Error("failed to get application instance health for upsert", zap.Int64("target_id", health.TargetID), zap.Error(err))
		return err
	}
	if err := r.db.WithContext(ctx).Create(health).Error; err != nil {
		logger.L().Error("failed to create application instance health", zap.Int64("target_id", health.TargetID), zap.Error(err))
		return err
	}
	return nil
}

func (r *ApplicationInstanceHealthRepository) DeleteByApplication(ctx context.Context, appID int64) error {
	if err := r.db.WithContext(ctx).
		Where("application_id = ?", appID).
		Delete(&domain.ApplicationInstanceHealth{}).Error; err != nil {
		logger.L().Error("failed to delete application instance health", zap.Int64("application_id", appID), zap.Error(err))
		return err
	}
	return nil
}

// DeleteByTargetID drops one instance's health record. Used when an instance is
// decommissioned: the health monitor stops probing a stopped instance, so a
// lingering record would freeze at its last (typically healthy) status and keep
// the UI showing the instance as green forever. Clearing it makes the instance
// read as unmonitored/unknown until a later deploy re-establishes health.
func (r *ApplicationInstanceHealthRepository) DeleteByTargetID(ctx context.Context, targetID int64) error {
	if err := r.db.WithContext(ctx).
		Where("target_id = ?", targetID).
		Delete(&domain.ApplicationInstanceHealth{}).Error; err != nil {
		logger.L().Error("failed to delete instance health by target", zap.Int64("target_id", targetID), zap.Error(err))
		return err
	}
	return nil
}
