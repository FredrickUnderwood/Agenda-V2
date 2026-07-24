package repository

import (
	"context"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

type EnvDeploymentRepository struct{ db *gorm.DB }

func NewEnvDeploymentRepository(db *gorm.DB) *EnvDeploymentRepository {
	return &EnvDeploymentRepository{db: db}
}

type ListEnvDeploymentsFilter struct {
	ApplicationID int64
	Env           domain.Environment
	Limit         int
	Offset        int
}

func (r *EnvDeploymentRepository) Create(ctx context.Context, d *domain.EnvDeployment) error {
	if d.StartedAt.IsZero() {
		d.StartedAt = time.Now().UTC()
	}
	if err := r.db.WithContext(ctx).Create(d).Error; err != nil {
		logger.L().Error("failed to create env deployment", zap.Int64("application_id", d.ApplicationID), zap.Error(err))
		return err
	}
	return nil
}

func (r *EnvDeploymentRepository) GetByID(ctx context.Context, id int64) (*domain.EnvDeployment, error) {
	var d domain.EnvDeployment
	if err := r.db.WithContext(ctx).First(&d, id).Error; err != nil {
		logger.L().Error("failed to get env deployment by id", zap.Int64("id", id), zap.Error(err))
		return nil, err
	}
	return &d, nil
}

func (r *EnvDeploymentRepository) List(ctx context.Context, f ListEnvDeploymentsFilter) ([]*domain.EnvDeployment, error) {
	q := r.db.WithContext(ctx).Model(&domain.EnvDeployment{}).Where("application_id = ?", f.ApplicationID)
	if f.Env != "" {
		q = q.Where("env = ?", f.Env)
	}
	if f.Limit <= 0 {
		f.Limit = 20
	}
	var out []*domain.EnvDeployment
	if err := q.Order("id DESC").Limit(f.Limit).Offset(f.Offset).Find(&out).Error; err != nil {
		logger.L().Error("failed to list env deployments", zap.Int64("application_id", f.ApplicationID), zap.Error(err))
		return nil, err
	}
	return out, nil
}

// UpdateAggregate writes the derived status + counts (+ finished_at once
// terminal). Callers recompute these from the child releases; this method just
// persists the recomputed snapshot, so it is safe to call repeatedly.
func (r *EnvDeploymentRepository) UpdateAggregate(ctx context.Context, id int64, status domain.EnvDeploymentStatus, total, success, failed int, finishedAt *time.Time) error {
	if err := r.db.WithContext(ctx).
		Model(&domain.EnvDeployment{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":        status,
			"total_count":   total,
			"success_count": success,
			"failed_count":  failed,
			"finished_at":   finishedAt,
		}).Error; err != nil {
		logger.L().Error("failed to update env deployment aggregate", zap.Int64("id", id), zap.Error(err))
		return err
	}
	return nil
}
