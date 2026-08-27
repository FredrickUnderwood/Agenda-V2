package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/nodeproxy"
	"github.com/FredrickUnderwood/agenda-v2/internal/redisguard"
)

// ErrWrongEngine is returned when a request reaches the path for the other
// engine — a SQL statement aimed at a Redis registration, or the reverse. It is
// the caller's mistake rather than a permission or connectivity problem, so it
// is worth telling them apart.
var ErrWrongEngine = errors.New("wrong engine for this database instance")

// defaultRedisDatabases is Redis's own default for the `databases` config, used
// when the registered account may not read that setting. Sixteen is what an
// unconfigured server has, so a console offering 0–15 is right far more often
// than it is wrong — and an index the server does not have simply errors.
const defaultRedisDatabases = 16

// RedisCommandRequest is one console command.
//
// DB is a pointer so that "the caller did not choose an index" stays distinct
// from "the caller chose 0", which is a real and very common index. Nil falls
// back to the instance's registered default.
type RedisCommandRequest struct {
	DB        *int   `json:"db"`
	Command   string `json:"command" binding:"required"`
	MaxRows   int    `json:"max_rows"`
	TimeoutMS int    `json:"timeout_ms"`
}

// RunRedisCommand validates, authorizes, executes and records one command.
//
// It is deliberately the same shape as Query, down to auditing every outcome
// including failures: the two paths differ in what they speak, not in who may
// run what or in what gets written down afterwards.
func (s *DatabaseQueryService) RunRedisCommand(ctx context.Context, p Principal, instanceID int64, req RedisCommandRequest) (*QueryResult, error) {
	inst, err := s.instances.Get(ctx, instanceID)
	if err != nil {
		return nil, ErrDatabaseInstanceNotFound
	}
	if inst.Engine != domain.DatabaseEngineRedis {
		return nil, fmt.Errorf("%w: %s is registered as %s; run SQL against it instead", ErrWrongEngine, inst.Name, inst.Engine)
	}
	if err := AuthorizeQuery(p, inst); err != nil {
		return nil, err
	}
	// Reject here as well as on the node so the operator gets the real reason
	// back rather than a relayed 400, and so a refused command never travels
	// with the Redis password attached.
	if _, err := redisguard.EnsureReadOnly(req.Command); err != nil {
		return nil, err
	}

	db := defaultRedisDB(inst)
	if req.DB != nil {
		db = *req.DB
	}

	resp, execErr := s.executeRedis(ctx, instanceID, db, req.Command, req.MaxRows, req.TimeoutMS)

	entry := &domain.DBQueryLog{
		InstanceID:   inst.ID,
		InstanceName: inst.Name,
		Env:          inst.Env,
		UserID:       p.UserID,
		Username:     p.Username,
		DatabaseName: "db" + strconv.Itoa(db),
		Statement:    truncateString(req.Command, statementMaxLen),
	}
	if execErr != nil {
		entry.Error = truncateString(execErr.Error(), 1024)
	} else {
		entry.Success = true
		entry.RowCount = resp.RowCount
		entry.DurationMS = resp.DurationMS
		entry.ResultSnapshot, entry.ResultTruncated = s.encodeSnapshot(resp)
	}
	s.recordAudit(ctx, entry)

	if execErr != nil {
		return nil, execErr
	}
	return &QueryResult{
		Columns:    resp.Columns,
		Rows:       resp.Rows,
		RowCount:   resp.RowCount,
		Truncated:  resp.Truncated,
		DurationMS: resp.DurationMS,
		Database:   entry.DatabaseName,
		QueryLogID: entry.ID,
	}, nil
}

// RedisDatabaseCount is how many numeric databases the console should offer.
//
// It asks the server (`CONFIG GET databases`) but treats a refusal as ordinary:
// a properly narrow ACL may well withhold CONFIG, and losing the DB picker over
// a setting nobody changed would be a poor trade. The fallback is logged, not
// surfaced as an error.
func (s *DatabaseQueryService) RedisDatabaseCount(ctx context.Context, p Principal, instanceID int64) (int, error) {
	inst, err := s.instances.Get(ctx, instanceID)
	if err != nil {
		return 0, ErrDatabaseInstanceNotFound
	}
	if inst.Engine != domain.DatabaseEngineRedis {
		return 0, fmt.Errorf("%w: %s is not a Redis instance", ErrWrongEngine, inst.Name)
	}
	if err := AuthorizeQuery(p, inst); err != nil {
		return 0, err
	}

	resp, err := s.executeRedis(ctx, instanceID, 0, "CONFIG GET databases", 4, 5000)
	if err != nil {
		logger.L().Info("could not read the databases setting; offering the Redis default",
			zap.Int64("instance_id", instanceID), zap.Error(err))
		return defaultRedisDatabases, nil
	}
	// CONFIG GET renders as field/value rows, so the count is the second cell
	// of the row naming the setting.
	for _, row := range resp.Rows {
		if len(row) == 2 && row[1] != nil {
			if n, convErr := strconv.Atoi(strings.TrimSpace(*row[1])); convErr == nil && n > 0 {
				return n, nil
			}
		}
	}
	return defaultRedisDatabases, nil
}

// testRedis is TestInstance's Redis half. PING proves the credentials work and
// is available to every account; the version is a best-effort extra, because an
// ACL narrow enough to withhold INFO should still pass a connection test.
func (s *DatabaseQueryService) testRedis(ctx context.Context, instanceID int64) (string, error) {
	if _, err := s.executeRedis(ctx, instanceID, 0, "PING", 1, 5000); err != nil {
		return "", err
	}
	resp, err := s.executeRedis(ctx, instanceID, 0, "INFO server", 1, 5000)
	if err != nil {
		return "", nil
	}
	if len(resp.Rows) > 0 && len(resp.Rows[0]) > 0 && resp.Rows[0][0] != nil {
		return redisVersionFromInfo(*resp.Rows[0][0]), nil
	}
	return "", nil
}

// redisVersionFromInfo pulls redis_version out of an INFO reply, which is one
// bulk string of "field:value" lines.
func redisVersionFromInfo(info string) string {
	for _, line := range strings.Split(info, "\n") {
		field, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if found && field == "redis_version" {
			return value
		}
	}
	return ""
}

// executeRedis resolves the instance and relays one command to its node,
// collapsing the node's "reachable but Redis failed" convention into an
// ordinary error exactly as execute does for SQL.
func (s *DatabaseQueryService) executeRedis(ctx context.Context, instanceID int64, db int, command string, maxRows, timeoutMS int) (*contract.NodeRedisCommandResponse, error) {
	resolved, err := s.instances.Resolve(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	nodeReq := contract.NodeRedisCommandRequest{
		Port:      resolved.Instance.Port,
		User:      resolved.Instance.Username,
		Password:  resolved.Password,
		DB:        db,
		Command:   command,
		MaxRows:   clampInt(maxRows, contract.NodeDBDefaultMaxRows, contract.NodeDBMaxRows),
		MaxBytes:  contract.NodeDBDefaultMaxBytes,
		TimeoutMS: clampInt(timeoutMS, contract.NodeDBDefaultTimeoutMS, contract.NodeDBMaxTimeoutMS),
	}

	resp, err := nodeproxy.ExecuteRedisCommand(ctx, resolved.AgentBaseURL, resolved.AgentToken, nodeReq)
	if err != nil {
		return nil, fmt.Errorf("could not reach the agenda-node agent on this database's machine: %w", err)
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	return resp, nil
}

// defaultRedisDB reads the instance's registered default index. The column is
// text (it holds a schema name for MySQL), so anything unparseable means "no
// default was set" and the console starts at 0.
func defaultRedisDB(inst *domain.DatabaseInstance) int {
	n, err := strconv.Atoi(strings.TrimSpace(inst.DefaultDatabase))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
