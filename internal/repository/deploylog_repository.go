package repository

import (
	"context"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

type DeployLogRepository struct{ db *gorm.DB }

func NewDeployLogRepository(db *gorm.DB) *DeployLogRepository {
	return &DeployLogRepository{db: db}
}

type ListLogsFilter struct {
	ApplicationID int64
	ReleaseID     int64
	Status        string
	Env           string
	Limit         int
	Offset        int
}

func (r *DeployLogRepository) Create(ctx context.Context, l *domain.DeployLog) error {
	if l.StartedAt.IsZero() {
		l.StartedAt = time.Now().UTC()
	}
	if err := r.db.WithContext(ctx).Create(l).Error; err != nil {
		logger.L().Error("failed to create deploy log", zap.Int64("application_id", l.ApplicationID), zap.Error(err))
		return err
	}
	return nil
}

func (r *DeployLogRepository) Finish(ctx context.Context, l *domain.DeployLog) error {
	now := time.Now().UTC()
	l.FinishedAt = &now
	if l.DurationMs == 0 {
		l.DurationMs = now.Sub(l.StartedAt).Milliseconds()
	}
	if err := r.db.WithContext(ctx).
		Model(&domain.DeployLog{}).
		Where("id = ?", l.ID).
		Updates(map[string]interface{}{
			"trigger_sha": l.TriggerSHA,
			"status":      l.Status,
			"output":      l.Output,
			"error_msg":   l.ErrorMsg,
			"finished_at": l.FinishedAt,
			"duration_ms": l.DurationMs,
		}).Error; err != nil {
		logger.L().Error("failed to finish deploy log", zap.Int64("id", l.ID), zap.Error(err))
		return err
	}
	return nil
}

// SetPauseRequested flips the pause flag for a run. The pipeline runner
// reads it between steps to decide whether to halt.
func (r *DeployLogRepository) SetPauseRequested(ctx context.Context, id int64, paused bool) error {
	if err := r.db.WithContext(ctx).
		Model(&domain.DeployLog{}).
		Where("id = ?", id).
		Update("pause_requested", paused).Error; err != nil {
		logger.L().Error("failed to set pause flag", zap.Int64("id", id), zap.Bool("paused", paused), zap.Error(err))
		return err
	}
	return nil
}

// MarkRunning resets a run back to running: clears FinishedAt/DurationMs/
// Output/ErrorMsg and sets status=running. Used on resume and retry.
func (r *DeployLogRepository) MarkRunning(ctx context.Context, id int64) error {
	if err := r.db.WithContext(ctx).
		Model(&domain.DeployLog{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":          domain.DeployStatusRunning,
			"pause_requested": false,
			"output":          "",
			"error_msg":       "",
			"finished_at":     nil,
			"duration_ms":     0,
		}).Error; err != nil {
		logger.L().Error("failed to mark run running", zap.Int64("id", id), zap.Error(err))
		return err
	}
	return nil
}

// MarkPaused writes the paused terminal (for now) status. triggerSHA is
// persisted too — git_pull may have already resolved it earlier in this same
// run, and once its step row is 'success' it won't run again on resume, so
// this is the only remaining chance to save that value before it's lost.
// The run can later be resumed by the application layer.
func (r *DeployLogRepository) MarkPaused(ctx context.Context, id int64, triggerSHA string) error {
	if err := r.db.WithContext(ctx).
		Model(&domain.DeployLog{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":          domain.DeployStatusPaused,
			"pause_requested": false,
			"trigger_sha":     triggerSHA,
		}).Error; err != nil {
		logger.L().Error("failed to mark run paused", zap.Int64("id", id), zap.Error(err))
		return err
	}
	return nil
}

func (r *DeployLogRepository) GetByID(ctx context.Context, id int64) (*domain.DeployLog, error) {
	var l domain.DeployLog
	if err := r.db.WithContext(ctx).First(&l, id).Error; err != nil {
		logger.L().Error("failed to get deploy log by id", zap.Int64("id", id), zap.Error(err))
		return nil, err
	}
	return &l, nil
}

func (r *DeployLogRepository) List(ctx context.Context, f ListLogsFilter) ([]*domain.DeployLog, error) {
	q := r.db.WithContext(ctx).Model(&domain.DeployLog{})
	if f.ApplicationID > 0 {
		q = q.Where("application_id = ?", f.ApplicationID)
	}
	if f.ReleaseID > 0 {
		q = q.Where("release_id = ?", f.ReleaseID)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.Env != "" {
		q = q.Where("env = ?", f.Env)
	}
	if f.Limit <= 0 {
		f.Limit = 20
	}
	var logs []*domain.DeployLog
	if err := q.Order("started_at DESC").
		Limit(f.Limit).
		Offset(f.Offset).
		Find(&logs).Error; err != nil {
		logger.L().Error("failed to list deploy logs", zap.Error(err))
		return nil, err
	}
	return logs, nil
}
