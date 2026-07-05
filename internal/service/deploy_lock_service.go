package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var ErrDeployLocked = errors.New("a deploy is already in progress for this application/env/instance")

// DeployLockService is a generic Redis-backed mutual-exclusion lock keyed by
// an arbitrary string. There is a single caller in this build — the release
// deploy path locks on "release:<app>:<env>:<instance>" — but the key is
// kept generic rather than typed to a specific entity.
type DeployLockService struct {
	rdb *redis.Client
}

func NewDeployLockService(rdb *redis.Client) *DeployLockService {
	return &DeployLockService{rdb: rdb}
}

func (s *DeployLockService) AcquireKey(ctx context.Context, key string, ttl time.Duration) (token string, err error) {
	token = uuid.NewString()
	ok, err := s.rdb.SetNX(ctx, lockKey(key), token, ttl).Result()
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrDeployLocked
	}
	return token, nil
}

// ReleaseKey deletes the lock only if the token matches (avoids releasing
// someone else's lock acquired after this one's TTL expired).
var releaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`)

func (s *DeployLockService) ReleaseKey(ctx context.Context, key string, token string) {
	_ = releaseScript.Run(ctx, s.rdb, []string{lockKey(key)}, token).Err()
}

func lockKey(key string) string {
	return fmt.Sprintf("agenda-v2:deploy_lock:%s", key)
}

// ReleaseLockKey builds the lock key for one (application, env, instance)
// deploy target — the granularity a release deploy locks on.
func ReleaseLockKey(applicationID int64, env, instanceName string) string {
	return fmt.Sprintf("release:%d:%s:%s", applicationID, env, instanceName)
}
