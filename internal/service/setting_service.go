package service

import (
	"context"
	"errors"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/secret"
)

// gitTokenKeyPrefix is the Setting key namespace for per-host git tokens, e.g.
// "git.token.github.com". GitToken resolves a host to its token through it.
const gitTokenKeyPrefix = "git.token."

// SettingRepository is the persistence contract the SettingService needs.
type SettingRepository interface {
	List(ctx context.Context) ([]*domain.Setting, error)
	GetByKey(ctx context.Context, key string) (*domain.Setting, error)
	Upsert(ctx context.Context, s *domain.Setting) error
	Delete(ctx context.Context, key string) error
}

// SettingService owns runtime-mutable configuration. It keeps a decrypted
// in-memory cache so hot-path reads (every git operation needs a token) never
// hit the DB, and refreshes the cache on every write. Secret values are
// encrypted before they reach the DB via the secret.Box.
type SettingService struct {
	repo SettingRepository
	box  *secret.Box

	mu    sync.RWMutex
	cache map[string]cachedSetting
}

type cachedSetting struct {
	value    string // decrypted
	isSecret bool
}

func NewSettingService(repo SettingRepository, box *secret.Box) *SettingService {
	return &SettingService{repo: repo, box: box, cache: make(map[string]cachedSetting)}
}

// Load populates the in-memory cache from the DB, decrypting secret values.
// Returns an error the caller may choose to treat as non-fatal (the server can
// still fall back to yaml config for anything not yet in the DB).
func (s *SettingService) Load(ctx context.Context) error {
	items, err := s.repo.List(ctx)
	if err != nil {
		return err
	}
	next := make(map[string]cachedSetting, len(items))
	for _, it := range items {
		value := it.Value
		if secret.IsEncrypted(value) {
			dec, err := s.box.Decrypt(value)
			if err != nil {
				logger.L().Error("failed to decrypt setting; skipping", zap.String("key", it.Key), zap.Error(err))
				continue
			}
			value = dec
		}
		next[it.Key] = cachedSetting{value: value, isSecret: it.IsSecret}
	}
	s.mu.Lock()
	s.cache = next
	s.mu.Unlock()
	return nil
}

// Get returns the decrypted value for key from the cache.
func (s *SettingService) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.cache[key]
	return c.value, ok
}

// GetByPrefix returns the decrypted values of every cached setting whose key
// has the given prefix, keyed by full setting key. Used by consumers that
// need to enumerate a whole namespace (e.g. AlertService's "alert.channel."
// keys) rather than resolve one known key like GitToken does.
func (s *SettingService) GetByPrefix(prefix string) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string)
	for k, c := range s.cache {
		if strings.HasPrefix(k, prefix) {
			out[k] = c.value
		}
	}
	return out
}

// SetSettingRequest is the input to Set.
type SetSettingRequest struct {
	Key      string             `json:"key"`
	Value    string             `json:"value"`
	Type     domain.SettingType `json:"type"`
	IsSecret bool               `json:"is_secret"`
}

// Set upserts a setting and refreshes the cache. Secret values are encrypted at
// rest; if no master key is configured the value is stored in plaintext and a
// warning is logged.
func (s *SettingService) Set(ctx context.Context, req SetSettingRequest, updatedBy int64) (*domain.Setting, error) {
	key := strings.TrimSpace(req.Key)
	if key == "" {
		return nil, errors.New("setting key is required")
	}
	typ := req.Type
	if typ == "" {
		typ = domain.SettingTypeString
	}

	stored := req.Value
	if req.IsSecret {
		if !s.box.Enabled() {
			logger.L().Warn("storing secret setting without encryption; set security.master_key to encrypt secrets at rest",
				zap.String("key", key))
		}
		enc, err := s.box.Encrypt(req.Value)
		if err != nil {
			return nil, err
		}
		stored = enc
	}

	setting := &domain.Setting{
		Key:       key,
		Value:     stored,
		Type:      typ,
		IsSecret:  req.IsSecret,
		UpdatedBy: updatedBy,
	}
	if err := s.repo.Upsert(ctx, setting); err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.cache[key] = cachedSetting{value: req.Value, isSecret: req.IsSecret}
	s.mu.Unlock()
	return setting, nil
}

// List returns all settings for the management API with secret values redacted.
func (s *SettingService) List(ctx context.Context) ([]*domain.Setting, error) {
	items, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.IsSecret || secret.IsEncrypted(it.Value) {
			it.Value = "***"
		}
	}
	return items, nil
}

// Delete removes a setting and evicts it from the cache.
func (s *SettingService) Delete(ctx context.Context, key string) error {
	if err := s.repo.Delete(ctx, key); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.cache, key)
	s.mu.Unlock()
	return nil
}

// GitToken resolves a host (e.g. "github.com") to its configured token, or ""
// if none is set. Safe for concurrent use — this is called from git operations.
func (s *SettingService) GitToken(host string) string {
	v, _ := s.Get(gitTokenKeyPrefix + host)
	return v
}

// SecretValues returns every non-empty secret value currently cached, so git
// output/log redaction can scrub them regardless of which host they belong to.
func (s *SettingService) SecretValues() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.cache))
	for _, c := range s.cache {
		if c.isSecret && c.value != "" {
			out = append(out, c.value)
		}
	}
	return out
}
