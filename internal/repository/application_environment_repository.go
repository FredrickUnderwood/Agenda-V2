package repository

import (
	"context"
	"errors"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

type ApplicationEnvironmentRepository struct{ db *gorm.DB }

func NewApplicationEnvironmentRepository(db *gorm.DB) *ApplicationEnvironmentRepository {
	return &ApplicationEnvironmentRepository{db: db}
}

// GetByApplicationEnv returns (nil, nil) when no row exists yet — callers
// treat a missing row as "no env-level overrides configured".
func (r *ApplicationEnvironmentRepository) GetByApplicationEnv(ctx context.Context, appID int64, env domain.Environment) (*domain.ApplicationEnvironment, error) {
	var row domain.ApplicationEnvironment
	err := r.db.WithContext(ctx).
		Where("application_id = ? AND env = ?", appID, env).
		First(&row).Error
	if err == nil {
		return &row, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	logger.L().Error("failed to get application environment",
		zap.Int64("application_id", appID), zap.String("env", string(env)), zap.Error(err))
	return nil, err
}

func (r *ApplicationEnvironmentRepository) Upsert(ctx context.Context, row *domain.ApplicationEnvironment) error {
	var existing domain.ApplicationEnvironment
	err := r.db.WithContext(ctx).
		Where("application_id = ? AND env = ?", row.ApplicationID, row.Env).
		First(&existing).Error
	if err == nil {
		row.ID = existing.ID
		if err := r.db.WithContext(ctx).
			Model(&domain.ApplicationEnvironment{}).
			Where("id = ?", existing.ID).
			Updates(map[string]interface{}{
				"env_vars_json": row.EnvVarsJSON,
				"config_json":   row.ConfigJSON,
			}).Error; err != nil {
			logger.L().Error("failed to update application environment", zap.Int64("id", row.ID), zap.Error(err))
			return err
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.L().Error("failed to get application environment for upsert", zap.Error(err))
		return err
	}
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		logger.L().Error("failed to create application environment",
			zap.Int64("application_id", row.ApplicationID), zap.String("env", string(row.Env)), zap.Error(err))
		return err
	}
	return nil
}

// ListByApplication returns every configured env-level row for one
// application. Environments with nothing configured simply have no row —
// callers fill the gaps rather than expecting one row per environment.
func (r *ApplicationEnvironmentRepository) ListByApplication(ctx context.Context, appID int64) ([]*domain.ApplicationEnvironment, error) {
	var rows []*domain.ApplicationEnvironment
	if err := r.db.WithContext(ctx).
		Where("application_id = ?", appID).
		Find(&rows).Error; err != nil {
		logger.L().Error("failed to list application environments", zap.Int64("application_id", appID), zap.Error(err))
		return nil, err
	}
	return rows, nil
}
