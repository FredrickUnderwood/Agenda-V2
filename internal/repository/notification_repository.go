package repository

import (
	"context"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

type NotificationRepository struct{ db *gorm.DB }

func NewNotificationRepository(db *gorm.DB) *NotificationRepository {
	return &NotificationRepository{db: db}
}

func (r *NotificationRepository) Create(ctx context.Context, n *domain.Notification) error {
	if err := r.db.WithContext(ctx).Create(n).Error; err != nil {
		logger.L().Error("failed to create notification", zap.Error(err))
		return err
	}
	return nil
}

// List returns notifications newest-first. unreadOnly filters to IsRead=false;
// limit<=0 means no limit.
func (r *NotificationRepository) List(ctx context.Context, unreadOnly bool, limit, offset int) ([]*domain.Notification, error) {
	q := r.db.WithContext(ctx).Order("created_at DESC")
	if unreadOnly {
		q = q.Where("is_read = ?", false)
	}
	if limit > 0 {
		q = q.Limit(limit).Offset(offset)
	}
	var items []*domain.Notification
	if err := q.Find(&items).Error; err != nil {
		logger.L().Error("failed to list notifications", zap.Error(err))
		return nil, err
	}
	return items, nil
}

func (r *NotificationRepository) CountUnread(ctx context.Context) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&domain.Notification{}).Where("is_read = ?", false).Count(&count).Error; err != nil {
		logger.L().Error("failed to count unread notifications", zap.Error(err))
		return 0, err
	}
	return count, nil
}

func (r *NotificationRepository) MarkRead(ctx context.Context, id int64) error {
	if err := r.db.WithContext(ctx).Model(&domain.Notification{}).Where("id = ?", id).Update("is_read", true).Error; err != nil {
		logger.L().Error("failed to mark notification read", zap.Int64("id", id), zap.Error(err))
		return err
	}
	return nil
}

func (r *NotificationRepository) MarkAllRead(ctx context.Context) error {
	if err := r.db.WithContext(ctx).Model(&domain.Notification{}).Where("is_read = ?", false).Update("is_read", true).Error; err != nil {
		logger.L().Error("failed to mark all notifications read", zap.Error(err))
		return err
	}
	return nil
}

func (r *NotificationRepository) Delete(ctx context.Context, id int64) error {
	if err := r.db.WithContext(ctx).Where("id = ?", id).Delete(&domain.Notification{}).Error; err != nil {
		logger.L().Error("failed to delete notification", zap.Int64("id", id), zap.Error(err))
		return err
	}
	return nil
}
