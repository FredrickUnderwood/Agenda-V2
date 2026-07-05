package repository

import (
	"context"
	"errors"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

type ApplicationReleaseRepository struct{ db *gorm.DB }

func NewApplicationReleaseRepository(db *gorm.DB) *ApplicationReleaseRepository {
	return &ApplicationReleaseRepository{db: db}
}

func (r *ApplicationReleaseRepository) Create(ctx context.Context, rel *domain.ApplicationRelease) error {
	if err := r.db.WithContext(ctx).Create(rel).Error; err != nil {
		logger.L().Error("failed to create application release",
			zap.Int64("application_id", rel.ApplicationID), zap.Error(err))
		return err
	}
	return nil
}

func (r *ApplicationReleaseRepository) GetByID(ctx context.Context, id int64) (*domain.ApplicationRelease, error) {
	var rel domain.ApplicationRelease
	if err := r.db.WithContext(ctx).First(&rel, id).Error; err != nil {
		logger.L().Error("failed to get application release by id", zap.Int64("id", id), zap.Error(err))
		return nil, err
	}
	return &rel, nil
}

type ListReleasesFilter struct {
	ApplicationID int64
	Env           domain.Environment
	InstanceName  string
	Status        domain.ReleaseStatus
	Limit         int
	Offset        int
}

func (r *ApplicationReleaseRepository) List(ctx context.Context, f ListReleasesFilter) ([]*domain.ApplicationRelease, error) {
	q := r.db.WithContext(ctx).Model(&domain.ApplicationRelease{}).Where("application_id = ?", f.ApplicationID)
	if f.Env != "" {
		q = q.Where("env = ?", f.Env)
	}
	if f.InstanceName != "" {
		q = q.Where("instance_name = ?", domain.NormalizeInstanceName(f.InstanceName))
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.Limit <= 0 {
		f.Limit = 20
	}
	var releases []*domain.ApplicationRelease
	if err := q.Order("id DESC").Limit(f.Limit).Offset(f.Offset).Find(&releases).Error; err != nil {
		logger.L().Error("failed to list application releases", zap.Int64("application_id", f.ApplicationID), zap.Error(err))
		return nil, err
	}
	return releases, nil
}

// GetLatestVerified returns the most recent verified release for
// (application_id, env, instance_name) — i.e. "what's currently live".
// Returns (nil, nil) when there is none yet.
func (r *ApplicationReleaseRepository) GetLatestVerified(ctx context.Context, appID int64, env domain.Environment, instanceName string) (*domain.ApplicationRelease, error) {
	var rel domain.ApplicationRelease
	err := r.db.WithContext(ctx).
		Where("application_id = ? AND env = ? AND instance_name = ? AND status = ?",
			appID, env, domain.NormalizeInstanceName(instanceName), domain.ReleaseStatusVerified).
		Order("verified_at DESC").
		First(&rel).Error
	if err == nil {
		return &rel, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	logger.L().Error("failed to get latest verified release",
		zap.Int64("application_id", appID), zap.String("env", string(env)), zap.String("instance_name", instanceName), zap.Error(err))
	return nil, err
}

// updateStatusAllowedColumns are the only columns UpdateStatus's extra map
// may touch, alongside status itself. Enforced (not just documented) so a
// future service-layer call can't accidentally write an arbitrary column —
// see application_target_repository.go's Upsert for the same guarantee via
// an explicit named-field map; extra needs to stay a map because the set of
// fields that change differs per transition (deployed_at/commit_sha on
// success, deploy_log_id on deploying, verified_at on verify...).
var updateStatusAllowedColumns = map[string]struct{}{
	"deploy_log_id": {},
	"deployed_at":   {},
	"verified_at":   {},
	"commit_sha":    {},
}

// UpdateStatus is a whitelisted-column status transition writer used by the
// service-layer state machine. extra carries additional columns that change
// alongside status (deployed_at, verified_at, deploy_log_id, commit_sha) —
// any other key is rejected rather than silently written.
func (r *ApplicationReleaseRepository) UpdateStatus(ctx context.Context, id int64, status domain.ReleaseStatus, extra map[string]interface{}) error {
	updates := map[string]interface{}{"status": status}
	for k, v := range extra {
		if _, ok := updateStatusAllowedColumns[k]; !ok {
			return errors.New("application_release: column " + k + " is not writable via UpdateStatus")
		}
		updates[k] = v
	}
	if err := r.db.WithContext(ctx).
		Model(&domain.ApplicationRelease{}).
		Where("id = ?", id).
		Updates(updates).Error; err != nil {
		logger.L().Error("failed to update application release status",
			zap.Int64("id", id), zap.String("status", string(status)), zap.Error(err))
		return err
	}
	return nil
}
