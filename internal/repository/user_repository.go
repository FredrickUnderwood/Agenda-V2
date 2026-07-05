package repository

import (
	"context"
	"errors"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

type UserRepository struct{ db *gorm.DB }

func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) Create(ctx context.Context, u *domain.User) error {
	if err := r.db.WithContext(ctx).Create(u).Error; err != nil {
		logger.L().Error("failed to create user", zap.String("username", u.Username), zap.Error(err))
		return err
	}
	return nil
}

func (r *UserRepository) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	var u domain.User
	if err := r.db.WithContext(ctx).First(&u, id).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// GetByUsername returns nil (no error) when the username does not exist, so the
// login path can treat "unknown user" and "wrong password" identically without
// logging a not-found as an error on every bad login attempt.
func (r *UserRepository) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	var u domain.User
	err := r.db.WithContext(ctx).Where("username = ?", username).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		logger.L().Error("failed to get user by username", zap.Error(err))
		return nil, err
	}
	return &u, nil
}

func (r *UserRepository) List(ctx context.Context) ([]*domain.User, error) {
	var users []*domain.User
	if err := r.db.WithContext(ctx).Order("id ASC").Find(&users).Error; err != nil {
		logger.L().Error("failed to list users", zap.Error(err))
		return nil, err
	}
	return users, nil
}

func (r *UserRepository) Update(ctx context.Context, u *domain.User) error {
	if err := r.db.WithContext(ctx).Save(u).Error; err != nil {
		logger.L().Error("failed to update user", zap.Int64("id", u.ID), zap.Error(err))
		return err
	}
	return nil
}

func (r *UserRepository) Delete(ctx context.Context, id int64) error {
	if err := r.db.WithContext(ctx).Delete(&domain.User{}, id).Error; err != nil {
		logger.L().Error("failed to delete user", zap.Int64("id", id), zap.Error(err))
		return err
	}
	return nil
}

func (r *UserRepository) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&domain.User{}).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}
