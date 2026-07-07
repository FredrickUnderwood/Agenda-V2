package service

import (
	"context"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
)

// NotificationService is the CRUD home for the in-app inbox ("站内信") —
// handlers go through this. AlertService, by contrast, depends on
// *repository.NotificationRepository directly to write new entries: that's
// plain data assembly (create a record alongside sending external alerts),
// the same reasoning ApplicationReleaseService documents for reading other
// domains via their repositories rather than their services.
type NotificationService struct {
	repo *repository.NotificationRepository
}

func NewNotificationService(repo *repository.NotificationRepository) *NotificationService {
	return &NotificationService{repo: repo}
}

func (s *NotificationService) List(ctx context.Context, unreadOnly bool, limit, offset int) ([]*domain.Notification, error) {
	return s.repo.List(ctx, unreadOnly, limit, offset)
}

func (s *NotificationService) CountUnread(ctx context.Context) (int64, error) {
	return s.repo.CountUnread(ctx)
}

func (s *NotificationService) MarkRead(ctx context.Context, id int64) error {
	return s.repo.MarkRead(ctx, id)
}

func (s *NotificationService) MarkAllRead(ctx context.Context) error {
	return s.repo.MarkAllRead(ctx)
}

func (s *NotificationService) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}
