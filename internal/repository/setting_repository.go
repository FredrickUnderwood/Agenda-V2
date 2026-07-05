package repository

import (
	"context"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

type SettingRepository struct{ db *gorm.DB }

func NewSettingRepository(db *gorm.DB) *SettingRepository {
	return &SettingRepository{db: db}
}

func (r *SettingRepository) List(ctx context.Context) ([]*domain.Setting, error) {
	var settings []*domain.Setting
	if err := r.db.WithContext(ctx).Order("setting_key ASC").Find(&settings).Error; err != nil {
		logger.L().Error("failed to list settings", zap.Error(err))
		return nil, err
	}
	return settings, nil
}

func (r *SettingRepository) GetByKey(ctx context.Context, key string) (*domain.Setting, error) {
	var s domain.Setting
	if err := r.db.WithContext(ctx).Where("setting_key = ?", key).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

// Upsert inserts the setting or, when its key already exists, updates the
// mutable columns in place. The unique index on setting_key drives the conflict.
func (r *SettingRepository) Upsert(ctx context.Context, s *domain.Setting) error {
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "setting_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "type", "is_secret", "updated_by", "updated_at"}),
	}).Create(s).Error; err != nil {
		logger.L().Error("failed to upsert setting", zap.String("key", s.Key), zap.Error(err))
		return err
	}
	return nil
}

func (r *SettingRepository) Delete(ctx context.Context, key string) error {
	if err := r.db.WithContext(ctx).Where("setting_key = ?", key).Delete(&domain.Setting{}).Error; err != nil {
		logger.L().Error("failed to delete setting", zap.String("key", key), zap.Error(err))
		return err
	}
	return nil
}
