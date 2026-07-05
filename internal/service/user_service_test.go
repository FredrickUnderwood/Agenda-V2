package service

import (
	"context"
	"errors"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
)

type fakeUserRepo struct {
	users  map[int64]*domain.User
	nextID int64
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{users: make(map[int64]*domain.User), nextID: 1}
}

func (f *fakeUserRepo) Create(_ context.Context, u *domain.User) error {
	for _, ex := range f.users {
		if ex.Username == u.Username {
			return errors.New("duplicate username")
		}
	}
	u.ID = f.nextID
	f.nextID++
	cp := *u
	f.users[u.ID] = &cp
	return nil
}

func (f *fakeUserRepo) GetByID(_ context.Context, id int64) (*domain.User, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return u, nil
}

func (f *fakeUserRepo) GetByUsername(_ context.Context, username string) (*domain.User, error) {
	for _, u := range f.users {
		if u.Username == username {
			return u, nil
		}
	}
	return nil, nil // not found → nil, nil (login treats as bad credentials)
}

func (f *fakeUserRepo) List(context.Context) ([]*domain.User, error) { return nil, nil }
func (f *fakeUserRepo) Update(context.Context, *domain.User) error   { return nil }
func (f *fakeUserRepo) Delete(context.Context, int64) error          { return nil }
func (f *fakeUserRepo) Count(context.Context) (int64, error)         { return int64(len(f.users)), nil }

func TestUserCreateHashesPasswordAndAuthenticates(t *testing.T) {
	svc := NewUserService(newFakeUserRepo())
	ctx := context.Background()

	u, err := svc.Create(ctx, CreateUserRequest{Username: "alice", Password: "hunter2", Role: "admin"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.PasswordHash == "hunter2" || u.PasswordHash == "" {
		t.Fatalf("password not hashed: %q", u.PasswordHash)
	}

	if _, err := svc.Authenticate(ctx, "alice", "hunter2"); err != nil {
		t.Fatalf("authenticate with right password: %v", err)
	}
	if _, err := svc.Authenticate(ctx, "alice", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password err = %v, want ErrInvalidCredentials", err)
	}
	if _, err := svc.Authenticate(ctx, "nobody", "hunter2"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user err = %v, want ErrInvalidCredentials", err)
	}
}

func TestUserCreateValidations(t *testing.T) {
	svc := NewUserService(newFakeUserRepo())
	ctx := context.Background()
	if _, err := svc.Create(ctx, CreateUserRequest{Username: "", Password: "longenough"}); err == nil {
		t.Fatal("empty username should fail")
	}
	if _, err := svc.Create(ctx, CreateUserRequest{Username: "bob", Password: "short"}); err == nil {
		t.Fatal("short password should fail")
	}
	if _, err := svc.Create(ctx, CreateUserRequest{Username: "bob", Password: "longenough", Role: "wizard"}); err == nil {
		t.Fatal("invalid role should fail")
	}
}

func TestEnsureBootstrapAdmin(t *testing.T) {
	repo := newFakeUserRepo()
	svc := NewUserService(repo)
	ctx := context.Background()

	if err := svc.EnsureBootstrapAdmin(ctx, "root", "rootpass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	u, err := svc.Authenticate(ctx, "root", "rootpass")
	if err != nil {
		t.Fatalf("bootstrapped admin cannot log in: %v", err)
	}
	if u.Role != "admin" {
		t.Fatalf("bootstrap user role = %q, want admin", u.Role)
	}

	// Idempotent: a second call with different creds must not create another user.
	if err := svc.EnsureBootstrapAdmin(ctx, "other", "otherpass"); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if n, _ := repo.Count(ctx); n != 1 {
		t.Fatalf("user count = %d, want 1 (bootstrap must be a no-op once users exist)", n)
	}
}
