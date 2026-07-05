package service

import (
	"context"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/secret"
)

// fakeSettingRepo is an in-memory SettingRepository keyed by setting key.
type fakeSettingRepo struct {
	items map[string]*domain.Setting
}

func newFakeSettingRepo() *fakeSettingRepo {
	return &fakeSettingRepo{items: make(map[string]*domain.Setting)}
}

func (f *fakeSettingRepo) List(context.Context) ([]*domain.Setting, error) {
	out := make([]*domain.Setting, 0, len(f.items))
	for _, v := range f.items {
		cp := *v
		out = append(out, &cp)
	}
	return out, nil
}

func (f *fakeSettingRepo) GetByKey(_ context.Context, key string) (*domain.Setting, error) {
	return f.items[key], nil
}

func (f *fakeSettingRepo) Upsert(_ context.Context, s *domain.Setting) error {
	cp := *s
	f.items[s.Key] = &cp
	return nil
}

func (f *fakeSettingRepo) Delete(_ context.Context, key string) error {
	delete(f.items, key)
	return nil
}

func TestSettingServiceSecretEncryptedAtRestPlainInCache(t *testing.T) {
	repo := newFakeSettingRepo()
	svc := NewSettingService(repo, secret.NewBox("master"))
	ctx := context.Background()

	const token = "ghp_secretvalue"
	if _, err := svc.Set(ctx, SetSettingRequest{
		Key:      "git.token.github.com",
		Value:    token,
		IsSecret: true,
	}, 42); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Stored form in the "DB" must be encrypted, never the plaintext token.
	stored := repo.items["git.token.github.com"]
	if stored == nil {
		t.Fatal("setting not persisted")
	}
	if !secret.IsEncrypted(stored.Value) {
		t.Fatalf("secret stored unencrypted: %q", stored.Value)
	}
	if stored.UpdatedBy != 42 {
		t.Fatalf("UpdatedBy = %d, want 42", stored.UpdatedBy)
	}

	// Cache holds the decrypted value for hot-path reads.
	if got := svc.GitToken("github.com"); got != token {
		t.Fatalf("GitToken = %q, want %q", got, token)
	}

	// The management List redacts the secret.
	listed, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].Value != "***" {
		t.Fatalf("List did not redact secret: %+v", listed)
	}

	// SecretValues surfaces the plaintext for redaction of git output.
	secrets := svc.SecretValues()
	if len(secrets) != 1 || secrets[0] != token {
		t.Fatalf("SecretValues = %v, want [%q]", secrets, token)
	}
}

func TestSettingServiceLoadDecryptsIntoCache(t *testing.T) {
	repo := newFakeSettingRepo()
	box := secret.NewBox("master")
	enc, err := box.Encrypt("ghp_persisted")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	repo.items["git.token.github.com"] = &domain.Setting{
		Key: "git.token.github.com", Value: enc, IsSecret: true,
	}

	svc := NewSettingService(repo, box)
	if err := svc.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := svc.GitToken("github.com"); got != "ghp_persisted" {
		t.Fatalf("after Load GitToken = %q, want ghp_persisted", got)
	}
}

func TestSettingServiceNonSecretRoundTrip(t *testing.T) {
	svc := NewSettingService(newFakeSettingRepo(), secret.NewBox(""))
	ctx := context.Background()
	if _, err := svc.Set(ctx, SetSettingRequest{Key: "deploy.default_timeout", Value: "10m"}, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if v, ok := svc.Get("deploy.default_timeout"); !ok || v != "10m" {
		t.Fatalf("Get = %q, %v", v, ok)
	}
	if got := svc.GitToken("github.com"); got != "" {
		t.Fatalf("unset GitToken = %q, want empty", got)
	}
}
