package repository

import (
	"context"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

type MachineFileRepository struct{ db *gorm.DB }

func NewMachineFileRepository(db *gorm.DB) *MachineFileRepository {
	return &MachineFileRepository{db: db}
}

func (r *MachineFileRepository) Create(ctx context.Context, f *domain.MachineFile) error {
	if err := r.db.WithContext(ctx).Create(f).Error; err != nil {
		logger.L().Error("failed to create machine file record",
			zap.Int64("machine_id", f.MachineID), zap.String("path", f.Path), zap.Error(err))
		return err
	}
	return nil
}

func (r *MachineFileRepository) GetByID(ctx context.Context, id int64) (*domain.MachineFile, error) {
	var f domain.MachineFile
	if err := r.db.WithContext(ctx).First(&f, id).Error; err != nil {
		return nil, err
	}
	return &f, nil
}

// ListByApplicationEnv returns every upload recorded for an application
// environment, newest first — the history, not just the current state.
func (r *MachineFileRepository) ListByApplicationEnv(ctx context.Context, appID int64, env domain.Environment) ([]*domain.MachineFile, error) {
	var out []*domain.MachineFile
	q := r.db.WithContext(ctx).Where("application_id = ? AND scope = ?", appID, domain.FileScopeAppEnv)
	if env != "" {
		q = q.Where("env = ?", env)
	}
	if err := q.Order("created_at DESC, id DESC").Find(&out).Error; err != nil {
		logger.L().Error("failed to list machine files by application env",
			zap.Int64("application_id", appID), zap.Error(err))
		return nil, err
	}
	return out, nil
}

// ListByMachine returns every upload recorded for one machine, newest first.
func (r *MachineFileRepository) ListByMachine(ctx context.Context, machineID int64) ([]*domain.MachineFile, error) {
	var out []*domain.MachineFile
	if err := r.db.WithContext(ctx).
		Where("machine_id = ?", machineID).
		Order("created_at DESC, id DESC").
		Find(&out).Error; err != nil {
		logger.L().Error("failed to list machine files by machine", zap.Int64("machine_id", machineID), zap.Error(err))
		return nil, err
	}
	return out, nil
}

// ListCurrent returns only the newest row per (machine_id, path) — the rows
// that describe what is on disk right now. Superseded rows are history and
// re-verifying them would report every replaced file as "changed".
func (r *MachineFileRepository) ListCurrent(ctx context.Context) ([]*domain.MachineFile, error) {
	var out []*domain.MachineFile
	sub := r.db.Model(&domain.MachineFile{}).
		Select("MAX(id)").
		Group("machine_id, path")
	if err := r.db.WithContext(ctx).
		Where("id IN (?)", sub).
		Order("machine_id, path").
		Find(&out).Error; err != nil {
		logger.L().Error("failed to list current machine files", zap.Error(err))
		return nil, err
	}
	return out, nil
}

// IsCurrent reports whether f is still the newest row for its (machine_id,
// path) — whether it describes the file on disk rather than a version that has
// since been replaced.
//
// It asks whether a newer row exists rather than taking a maximum over some
// subset of rows: a subset-based answer is only correct when the caller happens
// to hold every row of the group, which a single-record lookup never does.
func (r *MachineFileRepository) IsCurrent(ctx context.Context, f *domain.MachineFile) (bool, error) {
	var newer int64
	if err := r.db.WithContext(ctx).Model(&domain.MachineFile{}).
		Where("machine_id = ? AND path = ? AND id > ?", f.MachineID, f.Path, f.ID).
		Count(&newer).Error; err != nil {
		return false, err
	}
	return newer == 0, nil
}

// UpdateVerification records the outcome of a verification pass. It touches
// only the verification columns: the upload row itself is an immutable record
// of what was written and by whom.
func (r *MachineFileRepository) UpdateVerification(ctx context.Context, f *domain.MachineFile) error {
	if err := r.db.WithContext(ctx).Model(&domain.MachineFile{}).
		Where("id = ?", f.ID).
		Updates(map[string]any{
			"last_verified_at":   f.LastVerifiedAt,
			"last_verify_status": f.LastVerifyStatus,
			"last_verify_sha256": f.LastVerifySHA256,
			"last_verify_error":  f.LastVerifyError,
		}).Error; err != nil {
		logger.L().Error("failed to update machine file verification", zap.Int64("id", f.ID), zap.Error(err))
		return err
	}
	return nil
}
