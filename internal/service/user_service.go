package service

import (
	"context"
	"errors"
	"strings"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/FredrickUnderwood/agenda-v2/internal/auth"
	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
)

// ErrInvalidCredentials is returned for both an unknown username and a wrong
// password, so callers cannot distinguish the two.
var ErrInvalidCredentials = errors.New("invalid username or password")

const minPasswordLen = 6

// UserRepository is the persistence contract for users.
type UserRepository interface {
	Create(ctx context.Context, u *domain.User) error
	GetByID(ctx context.Context, id int64) (*domain.User, error)
	GetByUsername(ctx context.Context, username string) (*domain.User, error)
	List(ctx context.Context) ([]*domain.User, error)
	Update(ctx context.Context, u *domain.User) error
	Delete(ctx context.Context, id int64) error
	Count(ctx context.Context) (int64, error)
}

type UserService struct{ repo UserRepository }

func NewUserService(repo UserRepository) *UserService {
	return &UserService{repo: repo}
}

// CreateUserRequest is the input to Create.
type CreateUserRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

// Create hashes the password with bcrypt and stores the user.
func (s *UserService) Create(ctx context.Context, req CreateUserRequest) (*domain.User, error) {
	username := strings.TrimSpace(req.Username)
	if username == "" {
		return nil, errors.New("username is required")
	}
	if len(req.Password) < minPasswordLen {
		return nil, errors.New("password must be at least 6 characters")
	}
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = string(auth.RoleMember)
	}
	if role != string(auth.RoleAdmin) && role != string(auth.RoleMember) {
		return nil, errors.New("role must be admin or member")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	u := &domain.User{
		Username:     username,
		PasswordHash: string(hash),
		DisplayName:  strings.TrimSpace(req.DisplayName),
		Role:         role,
		IsActive:     true,
	}
	if err := s.repo.Create(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// Authenticate verifies a username/password and returns the user on success.
func (s *UserService) Authenticate(ctx context.Context, username, password string) (*domain.User, error) {
	u, err := s.repo.GetByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrInvalidCredentials
	}
	if !u.IsActive {
		return nil, errors.New("user is disabled")
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, ErrInvalidCredentials
	}
	return u, nil
}

func (s *UserService) GetByID(ctx context.Context, id int64) (*domain.User, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *UserService) List(ctx context.Context) ([]*domain.User, error) {
	return s.repo.List(ctx)
}

func (s *UserService) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}

// EnsureBootstrapAdmin creates an initial admin when no users exist yet, so a
// fresh install has a way in. It is a no-op once any user exists.
func (s *UserService) EnsureBootstrapAdmin(ctx context.Context, username, password string) error {
	n, err := s.repo.Count(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		logger.L().Warn("no users exist and no bootstrap admin configured; set auth.bootstrap_admin_username / auth.bootstrap_admin_password to create the first admin")
		return nil
	}
	if _, err := s.Create(ctx, CreateUserRequest{
		Username:    username,
		Password:    password,
		DisplayName: "Administrator",
		Role:        string(auth.RoleAdmin),
	}); err != nil {
		return err
	}
	logger.L().Info("bootstrap admin created", zap.String("username", username))
	return nil
}
