package repository

import (
	"context"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

type DBQueryLogRepository struct{ db *gorm.DB }

func NewDBQueryLogRepository(db *gorm.DB) *DBQueryLogRepository {
	return &DBQueryLogRepository{db: db}
}

// DBQueryLogFilter narrows an audit listing. UserID is a pointer rather than a
// zero-means-all int because 0 is a real user id here (the service token and
// dev mode both log as user 0), so "no filter" and "user 0" must stay
// distinguishable.
type DBQueryLogFilter struct {
	UserID     *int64
	InstanceID int64
	Limit      int
	Offset     int
}

func (r *DBQueryLogRepository) Create(ctx context.Context, entry *domain.DBQueryLog) error {
	if err := r.db.WithContext(ctx).Create(entry).Error; err != nil {
		logger.L().Error("failed to write db query log", zap.Int64("instance_id", entry.InstanceID), zap.Error(err))
		return err
	}
	return nil
}

func (r *DBQueryLogRepository) GetByID(ctx context.Context, id int64) (*domain.DBQueryLog, error) {
	var entry domain.DBQueryLog
	if err := r.db.WithContext(ctx).First(&entry, id).Error; err != nil {
		return nil, err
	}
	return &entry, nil
}

// List returns audit entries newest-first. The caller is responsible for
// setting Filter.UserID when the requester may only see their own history —
// the narrowing has to happen in this query, never by discarding rows after
// they have already been read.
func (r *DBQueryLogRepository) List(ctx context.Context, f DBQueryLogFilter) ([]*domain.DBQueryLog, error) {
	q := r.applyFilter(r.db.WithContext(ctx), f).Order("created_at DESC, id DESC")
	if f.Limit > 0 {
		q = q.Limit(f.Limit).Offset(f.Offset)
	}
	var items []*domain.DBQueryLog
	if err := q.Find(&items).Error; err != nil {
		logger.L().Error("failed to list db query logs", zap.Error(err))
		return nil, err
	}
	return items, nil
}

func (r *DBQueryLogRepository) Count(ctx context.Context, f DBQueryLogFilter) (int64, error) {
	var total int64
	q := r.applyFilter(r.db.WithContext(ctx).Model(&domain.DBQueryLog{}), f)
	if err := q.Count(&total).Error; err != nil {
		logger.L().Error("failed to count db query logs", zap.Error(err))
		return 0, err
	}
	return total, nil
}

// DeleteOlderThan drops audit entries past the retention window. Query results
// are stored in these rows, so this is what keeps real database contents from
// accumulating in the control-plane database indefinitely.
func (r *DBQueryLogRepository) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Where("created_at < ?", cutoff).Delete(&domain.DBQueryLog{})
	if res.Error != nil {
		logger.L().Error("failed to purge db query logs", zap.Error(res.Error))
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

func (r *DBQueryLogRepository) applyFilter(q *gorm.DB, f DBQueryLogFilter) *gorm.DB {
	if f.UserID != nil {
		q = q.Where("user_id = ?", *f.UserID)
	}
	if f.InstanceID > 0 {
		q = q.Where("instance_id = ?", f.InstanceID)
	}
	return q
}
