package repository

import (
	"context"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

type DatabaseInstanceRepository struct{ db *gorm.DB }

func NewDatabaseInstanceRepository(db *gorm.DB) *DatabaseInstanceRepository {
	return &DatabaseInstanceRepository{db: db}
}

func (r *DatabaseInstanceRepository) Create(ctx context.Context, inst *domain.DatabaseInstance) error {
	if err := r.db.WithContext(ctx).Create(inst).Error; err != nil {
		logger.L().Error("failed to create database instance", zap.String("name", inst.Name), zap.Error(err))
		return err
	}
	return nil
}

func (r *DatabaseInstanceRepository) GetByID(ctx context.Context, id int64) (*domain.DatabaseInstance, error) {
	var inst domain.DatabaseInstance
	if err := r.db.WithContext(ctx).First(&inst, id).Error; err != nil {
		logger.L().Error("failed to get database instance by id", zap.Int64("id", id), zap.Error(err))
		return nil, err
	}
	return &inst, nil
}

func (r *DatabaseInstanceRepository) List(ctx context.Context) ([]*domain.DatabaseInstance, error) {
	var items []*domain.DatabaseInstance
	if err := r.db.WithContext(ctx).Order("name").Find(&items).Error; err != nil {
		logger.L().Error("failed to list database instances", zap.Error(err))
		return nil, err
	}
	return items, nil
}

func (r *DatabaseInstanceRepository) Update(ctx context.Context, inst *domain.DatabaseInstance) error {
	if err := r.db.WithContext(ctx).Save(inst).Error; err != nil {
		logger.L().Error("failed to update database instance", zap.Int64("id", inst.ID), zap.Error(err))
		return err
	}
	return nil
}

func (r *DatabaseInstanceRepository) Delete(ctx context.Context, id int64) error {
	if err := r.db.WithContext(ctx).Delete(&domain.DatabaseInstance{}, id).Error; err != nil {
		logger.L().Error("failed to delete database instance", zap.Int64("id", id), zap.Error(err))
		return err
	}
	return nil
}
