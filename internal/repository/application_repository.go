package repository

import (
	"context"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

type ApplicationRepository struct{ db *gorm.DB }

func NewApplicationRepository(db *gorm.DB) *ApplicationRepository {
	return &ApplicationRepository{db: db}
}

func (r *ApplicationRepository) Create(ctx context.Context, app *domain.Application) error {
	if err := r.db.WithContext(ctx).Create(app).Error; err != nil {
		logger.L().Error("failed to create application", zap.String("name", app.Name), zap.Error(err))
		return err
	}
	return nil
}

func (r *ApplicationRepository) GetByID(ctx context.Context, id int64) (*domain.Application, error) {
	var app domain.Application
	if err := r.db.WithContext(ctx).First(&app, id).Error; err != nil {
		logger.L().Error("failed to get application by id", zap.Int64("id", id), zap.Error(err))
		return nil, err
	}
	return &app, nil
}

func (r *ApplicationRepository) List(ctx context.Context) ([]*domain.Application, error) {
	var apps []*domain.Application
	if err := r.db.WithContext(ctx).Order("id DESC").Find(&apps).Error; err != nil {
		logger.L().Error("failed to list applications", zap.Error(err))
		return nil, err
	}
	return apps, nil
}

func (r *ApplicationRepository) Update(ctx context.Context, app *domain.Application) error {
	if err := r.db.WithContext(ctx).Save(app).Error; err != nil {
		logger.L().Error("failed to update application", zap.Int64("id", app.ID), zap.Error(err))
		return err
	}
	return nil
}

func (r *ApplicationRepository) Delete(ctx context.Context, id int64) error {
	if err := r.db.WithContext(ctx).Delete(&domain.Application{}, id).Error; err != nil {
		logger.L().Error("failed to delete application", zap.Int64("id", id), zap.Error(err))
		return err
	}
	return nil
}
