package repository

import (
	"context"
	"errors"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

type ApplicationTargetRepository struct{ db *gorm.DB }

func NewApplicationTargetRepository(db *gorm.DB) *ApplicationTargetRepository {
	return &ApplicationTargetRepository{db: db}
}

func (r *ApplicationTargetRepository) ListByApplication(ctx context.Context, appID int64) ([]*domain.ApplicationEnvTarget, error) {
	var targets []*domain.ApplicationEnvTarget
	if err := r.db.WithContext(ctx).
		Where("application_id = ?", appID).
		Order("CASE env WHEN 'prod' THEN 1 WHEN 'stage' THEN 2 WHEN 'test' THEN 3 ELSE 4 END, instance_name").
		Find(&targets).Error; err != nil {
		logger.L().Error("failed to list application targets", zap.Int64("application_id", appID), zap.Error(err))
		return nil, err
	}
	return targets, nil
}

func (r *ApplicationTargetRepository) ListByApplicationIDs(ctx context.Context, appIDs []int64) ([]*domain.ApplicationEnvTarget, error) {
	if len(appIDs) == 0 {
		return nil, nil
	}
	var targets []*domain.ApplicationEnvTarget
	if err := r.db.WithContext(ctx).
		Where("application_id IN ?", appIDs).
		Order("application_id, CASE env WHEN 'prod' THEN 1 WHEN 'stage' THEN 2 WHEN 'test' THEN 3 ELSE 4 END, instance_name").
		Find(&targets).Error; err != nil {
		logger.L().Error("failed to list application targets by apps", zap.Error(err))
		return nil, err
	}
	return targets, nil
}

func (r *ApplicationTargetRepository) GetByID(ctx context.Context, id int64) (*domain.ApplicationEnvTarget, error) {
	var target domain.ApplicationEnvTarget
	if err := r.db.WithContext(ctx).First(&target, id).Error; err != nil {
		logger.L().Error("failed to get application target by id", zap.Int64("id", id), zap.Error(err))
		return nil, err
	}
	return &target, nil
}

func (r *ApplicationTargetRepository) GetByApplicationEnv(ctx context.Context, appID int64, env domain.Environment) (*domain.ApplicationEnvTarget, error) {
	return r.GetByApplicationEnvInstance(ctx, appID, env, domain.DefaultInstanceName)
}

func (r *ApplicationTargetRepository) GetByApplicationEnvInstance(ctx context.Context, appID int64, env domain.Environment, instanceName string) (*domain.ApplicationEnvTarget, error) {
	var target domain.ApplicationEnvTarget
	instanceName = domain.NormalizeInstanceName(instanceName)
	if err := r.db.WithContext(ctx).
		Where("application_id = ? AND env = ? AND instance_name = ?", appID, env, instanceName).
		First(&target).Error; err != nil {
		logger.L().Error("failed to get application target",
			zap.Int64("application_id", appID),
			zap.String("env", string(env)),
			zap.String("instance_name", instanceName),
			zap.Error(err))
		return nil, err
	}
	return &target, nil
}

func (r *ApplicationTargetRepository) ListHealthCheckEnabled(ctx context.Context) ([]*domain.ApplicationEnvTarget, error) {
	var targets []*domain.ApplicationEnvTarget
	if err := r.db.WithContext(ctx).
		Where("enabled = ? AND health_check_enabled = ?", true, true).
		Order("application_id, env, instance_name").
		Find(&targets).Error; err != nil {
		logger.L().Error("failed to list health check enabled targets", zap.Error(err))
		return nil, err
	}
	return targets, nil
}

func (r *ApplicationTargetRepository) ListEnabledByMachinePort(ctx context.Context, machineID int64, port int) ([]*domain.ApplicationEnvTarget, error) {
	var targets []*domain.ApplicationEnvTarget
	if err := r.db.WithContext(ctx).
		Where("enabled = ? AND machine_id = ? AND port = ?", true, machineID, port).
		Find(&targets).Error; err != nil {
		logger.L().Error("failed to list enabled targets by machine port",
			zap.Int64("machine_id", machineID), zap.Int("port", port), zap.Error(err))
		return nil, err
	}
	return targets, nil
}

func (r *ApplicationTargetRepository) Upsert(ctx context.Context, target *domain.ApplicationEnvTarget) error {
	var existing domain.ApplicationEnvTarget
	target.InstanceName = domain.NormalizeInstanceName(target.InstanceName)
	err := r.db.WithContext(ctx).
		Where("application_id = ? AND env = ? AND instance_name = ?", target.ApplicationID, target.Env, target.InstanceName).
		First(&existing).Error
	if err == nil {
		target.ID = existing.ID
		if err := r.db.WithContext(ctx).
			Model(&domain.ApplicationEnvTarget{}).
			Where("id = ?", existing.ID).
			Updates(map[string]interface{}{
				"display_name":                   target.DisplayName,
				"machine_id":                     target.MachineID,
				"port":                           target.Port,
				"enabled":                        target.Enabled,
				"health_check_enabled":           target.HealthCheckEnabled,
				"health_check_type":              target.HealthCheckType,
				"health_check_scheme":            target.HealthCheckScheme,
				"health_check_host":              target.HealthCheckHost,
				"health_check_url":               target.HealthCheckURL,
				"health_check_path":              target.HealthCheckPath,
				"health_check_method":            target.HealthCheckMethod,
				"health_check_expected_status":   target.HealthCheckExpectedStatus,
				"health_check_timeout_ms":        target.HealthCheckTimeoutMS,
				"health_check_interval_sec":      target.HealthCheckIntervalSec,
				"health_check_failure_threshold": target.HealthCheckFailureThreshold,
				"health_check_success_threshold": target.HealthCheckSuccessThreshold,
				"env_override_json":              target.EnvOverrideJSON,
			}).Error; err != nil {
			logger.L().Error("failed to update application target", zap.Int64("id", target.ID), zap.Error(err))
			return err
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.L().Error("failed to get application target for upsert", zap.Error(err))
		return err
	}
	if err := r.db.WithContext(ctx).Create(target).Error; err != nil {
		logger.L().Error("failed to create application target",
			zap.Int64("application_id", target.ApplicationID),
			zap.String("env", string(target.Env)),
			zap.String("instance_name", target.InstanceName),
			zap.Error(err))
		return err
	}
	return nil
}

func (r *ApplicationTargetRepository) DeleteByApplication(ctx context.Context, appID int64) error {
	if err := r.db.WithContext(ctx).
		Where("application_id = ?", appID).
		Delete(&domain.ApplicationEnvTarget{}).Error; err != nil {
		logger.L().Error("failed to delete application targets", zap.Int64("application_id", appID), zap.Error(err))
		return err
	}
	return nil
}

func (r *ApplicationTargetRepository) DeleteByApplicationEnvInstance(ctx context.Context, appID int64, env domain.Environment, instanceName string) error {
	instanceName = domain.NormalizeInstanceName(instanceName)
	if err := r.db.WithContext(ctx).
		Where("application_id = ? AND env = ? AND instance_name = ?", appID, env, instanceName).
		Delete(&domain.ApplicationEnvTarget{}).Error; err != nil {
		logger.L().Error("failed to delete application target",
			zap.Int64("application_id", appID),
			zap.String("env", string(env)),
			zap.String("instance_name", instanceName),
			zap.Error(err))
		return err
	}
	return nil
}
