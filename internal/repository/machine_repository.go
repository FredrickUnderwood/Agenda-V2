package repository

import (
	"context"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

type MachineRepository struct{ db *gorm.DB }

func NewMachineRepository(db *gorm.DB) *MachineRepository {
	return &MachineRepository{db: db}
}

func (r *MachineRepository) Create(ctx context.Context, m *domain.Machine) error {
	if err := r.db.WithContext(ctx).Create(m).Error; err != nil {
		logger.L().Error("failed to create machine", zap.String("name", m.Name), zap.Error(err))
		return err
	}
	return nil
}

func (r *MachineRepository) GetByID(ctx context.Context, id int64) (*domain.Machine, error) {
	var m domain.Machine
	if err := r.db.WithContext(ctx).First(&m, id).Error; err != nil {
		logger.L().Error("failed to get machine by id", zap.Int64("id", id), zap.Error(err))
		return nil, err
	}
	return &m, nil
}

func (r *MachineRepository) GetByName(ctx context.Context, name string) (*domain.Machine, error) {
	var m domain.Machine
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&m).Error; err != nil {
		logger.L().Error("failed to get machine by name", zap.String("name", name), zap.Error(err))
		return nil, err
	}
	return &m, nil
}

func (r *MachineRepository) List(ctx context.Context) ([]*domain.Machine, error) {
	var machines []*domain.Machine
	if err := r.db.WithContext(ctx).Order("created_at DESC").Find(&machines).Error; err != nil {
		logger.L().Error("failed to list machines", zap.Error(err))
		return nil, err
	}
	return machines, nil
}

func (r *MachineRepository) Update(ctx context.Context, m *domain.Machine) error {
	if err := r.db.WithContext(ctx).Save(m).Error; err != nil {
		logger.L().Error("failed to update machine", zap.Int64("id", m.ID), zap.Error(err))
		return err
	}
	return nil
}

func (r *MachineRepository) Delete(ctx context.Context, id int64) error {
	if err := r.db.WithContext(ctx).Delete(&domain.Machine{}, id).Error; err != nil {
		logger.L().Error("failed to delete machine", zap.Int64("id", id), zap.Error(err))
		return err
	}
	return nil
}

// UpdateHeartbeat records an agent heartbeat, touching only the heartbeat
// timestamp and version columns (a targeted update, not a full Save, so a
// concurrent Machine edit is not clobbered). Returns gorm.ErrRecordNotFound
// when no such machine exists.
func (r *MachineRepository) UpdateHeartbeat(ctx context.Context, id int64, version string, at time.Time) error {
	res := r.db.WithContext(ctx).Model(&domain.Machine{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"agent_last_heartbeat_at": at,
			"agent_version":           version,
		})
	if res.Error != nil {
		logger.L().Error("failed to update heartbeat", zap.Int64("id", id), zap.Error(res.Error))
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
