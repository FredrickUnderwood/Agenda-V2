package repository

import (
	"context"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

type AlertRuleRepository struct{ db *gorm.DB }

func NewAlertRuleRepository(db *gorm.DB) *AlertRuleRepository {
	return &AlertRuleRepository{db: db}
}

func (r *AlertRuleRepository) Create(ctx context.Context, rule *domain.AlertRule) error {
	if err := r.db.WithContext(ctx).Create(rule).Error; err != nil {
		logger.L().Error("failed to create alert rule", zap.String("name", rule.Name), zap.Error(err))
		return err
	}
	return nil
}

func (r *AlertRuleRepository) Update(ctx context.Context, rule *domain.AlertRule) error {
	if err := r.db.WithContext(ctx).Save(rule).Error; err != nil {
		logger.L().Error("failed to update alert rule", zap.Int64("id", rule.ID), zap.Error(err))
		return err
	}
	return nil
}

func (r *AlertRuleRepository) GetByID(ctx context.Context, id int64) (*domain.AlertRule, error) {
	var rule domain.AlertRule
	if err := r.db.WithContext(ctx).First(&rule, id).Error; err != nil {
		logger.L().Error("failed to get alert rule by id", zap.Int64("id", id), zap.Error(err))
		return nil, err
	}
	return &rule, nil
}

func (r *AlertRuleRepository) Delete(ctx context.Context, id int64) error {
	if err := r.db.WithContext(ctx).Delete(&domain.AlertRule{}, id).Error; err != nil {
		logger.L().Error("failed to delete alert rule", zap.Int64("id", id), zap.Error(err))
		return err
	}
	return nil
}

func (r *AlertRuleRepository) List(ctx context.Context) ([]*domain.AlertRule, error) {
	var rules []*domain.AlertRule
	if err := r.db.WithContext(ctx).Order("name").Find(&rules).Error; err != nil {
		logger.L().Error("failed to list alert rules", zap.Error(err))
		return nil, err
	}
	return rules, nil
}

func (r *AlertRuleRepository) ListEnabled(ctx context.Context) ([]*domain.AlertRule, error) {
	var rules []*domain.AlertRule
	if err := r.db.WithContext(ctx).Where("enabled = ?", true).Order("name").Find(&rules).Error; err != nil {
		logger.L().Error("failed to list enabled alert rules", zap.Error(err))
		return nil, err
	}
	return rules, nil
}
