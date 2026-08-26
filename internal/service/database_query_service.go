package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/nodeproxy"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
	"github.com/FredrickUnderwood/agenda-v2/internal/secret"
	"github.com/FredrickUnderwood/agenda-v2/internal/sqlguard"
)

// ErrQueryForbidden is returned when a principal may not query an instance in
// that environment.
var ErrQueryForbidden = errors.New("querying this database requires admin privileges")

// ErrAuditEntryNotFound covers both "no such entry" and "not yours". The two
// are deliberately indistinguishable: telling a caller that an entry exists but
// belongs to somebody else is itself a disclosure.
var ErrAuditEntryNotFound = errors.New("query log entry not found")

// SettingQueryLogRetentionDays is how long audit entries — which hold real
// query results — are kept before PurgeExpiredLogs removes them.
const SettingQueryLogRetentionDays = "rds.query_log_retention_days"

const defaultQueryLogRetentionDays = 30

// Snapshot caps. An audit entry stores what a query returned so its author can
// look at it again later, but it is an audit trail, not a second copy of the
// database — a large result is kept in outline only.
const (
	snapshotMaxRows  = 200
	snapshotMaxBytes = 256 << 10
	statementMaxLen  = 8 << 10
)

// Principal is the acting user, flattened to what authorization here actually
// needs. The handler derives it from the request identity, which keeps this
// service free of any dependency on how authentication works — and lets the
// dev-mode "no auth configured" case be expressed as simply IsAdmin: true.
type Principal struct {
	UserID   int64
	Username string
	IsAdmin  bool
}

// AuthorizeQuery decides whether p may run a statement against inst.
//
// This is the extension point the environment split was chosen for: today the
// rule is coarse — test databases are open to any authenticated user, anything
// else is admin-only — and a finer per-instance ACL replaces the body of this
// function without touching the handlers or the query path.
func AuthorizeQuery(p Principal, inst *domain.DatabaseInstance) error {
	if p.IsAdmin {
		return nil
	}
	if inst.Env == domain.EnvironmentTest {
		return nil
	}
	return fmt.Errorf("%w (this instance is registered as %s)", ErrQueryForbidden, inst.Env)
}

// settingGetter is the slice of SettingService the retention window needs.
type settingGetter interface {
	Get(key string) (string, bool)
}

// DatabaseQueryService runs read-only statements against registered databases
// by relaying them through the target machine's agenda-node, and records every
// attempt in the audit trail.
type DatabaseQueryService struct {
	instances *DatabaseInstanceService
	auditLogs *repository.DBQueryLogRepository
	settings  settingGetter
	box       *secret.Box
}

func NewDatabaseQueryService(
	instances *DatabaseInstanceService,
	auditLogs *repository.DBQueryLogRepository,
	settings settingGetter,
	box *secret.Box,
) *DatabaseQueryService {
	return &DatabaseQueryService{instances: instances, auditLogs: auditLogs, settings: settings, box: box}
}

type QueryRequest struct {
	Database  string `json:"database"`
	SQL       string `json:"sql" binding:"required"`
	MaxRows   int    `json:"max_rows"`
	TimeoutMS int    `json:"timeout_ms"`
}

// QueryResult is the API shape of a completed query. It carries the audit id so
// the console can link straight from a result to its history entry.
type QueryResult struct {
	Columns    []contract.NodeDBColumn `json:"columns"`
	Rows       [][]*string             `json:"rows"`
	RowCount   int                     `json:"row_count"`
	Truncated  bool                    `json:"truncated"`
	DurationMS int                     `json:"duration_ms"`
	Database   string                  `json:"database"`
	QueryLogID int64                   `json:"query_log_id"`
}

// Query validates, authorizes, executes and records one statement.
//
// Every outcome after authorization is audited, failures included: "who tried
// to run what" is exactly the question an audit trail exists to answer, and
// recording only successes would answer it wrongly.
func (s *DatabaseQueryService) Query(ctx context.Context, p Principal, instanceID int64, req QueryRequest) (*QueryResult, error) {
	inst, err := s.instances.Get(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if err := AuthorizeQuery(p, inst); err != nil {
		return nil, err
	}
	// Reject here as well as on the node so the operator gets the real reason
	// back rather than a relayed 400, and so an obviously bad statement never
	// travels with the database password attached.
	if _, err := sqlguard.EnsureReadOnly(req.SQL); err != nil {
		return nil, err
	}

	database := strings.TrimSpace(req.Database)
	if database == "" {
		database = inst.DefaultDatabase
	}

	resp, execErr := s.execute(ctx, instanceID, database, req.SQL, req.MaxRows, req.TimeoutMS)

	entry := &domain.DBQueryLog{
		InstanceID:   inst.ID,
		InstanceName: inst.Name,
		Env:          inst.Env,
		UserID:       p.UserID,
		Username:     p.Username,
		DatabaseName: database,
		Statement:    truncateString(req.SQL, statementMaxLen),
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
		Database:   database,
		QueryLogID: entry.ID,
	}, nil
}

// execute resolves the instance and relays one statement to its node. The
// node's own "reachable but the database failed" convention is collapsed into
// an ordinary error here, because from a caller's point of view both mean the
// query did not run — the distinction is preserved in the message.
func (s *DatabaseQueryService) execute(ctx context.Context, instanceID int64, database, stmt string, maxRows, timeoutMS int) (*contract.NodeDBQueryResponse, error) {
	resolved, err := s.instances.Resolve(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	nodeReq := contract.NodeDBQueryRequest{
		Driver:    string(resolved.Instance.Engine),
		Port:      resolved.Instance.Port,
		User:      resolved.Instance.Username,
		Password:  resolved.Password,
		Database:  database,
		SQL:       stmt,
		MaxRows:   clampInt(maxRows, contract.NodeDBDefaultMaxRows, contract.NodeDBMaxRows),
		MaxBytes:  contract.NodeDBDefaultMaxBytes,
		TimeoutMS: clampInt(timeoutMS, contract.NodeDBDefaultTimeoutMS, contract.NodeDBMaxTimeoutMS),
	}

	resp, err := nodeproxy.ExecuteQuery(ctx, resolved.AgentBaseURL, resolved.AgentToken, nodeReq)
	if err != nil {
		return nil, fmt.Errorf("could not reach the agenda-node agent on this database's machine: %w", err)
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	return resp, nil
}

// TestInstance checks that the node can actually open a connection with the
// stored credentials, and returns the server version as proof it got through.
// It runs a real statement over the ordinary query path but is not audited: it
// reads no data, and an operator saving a form should not fill the trail.
func (s *DatabaseQueryService) TestInstance(ctx context.Context, instanceID int64) (version string, err error) {
	resp, err := s.execute(ctx, instanceID, "", "SELECT VERSION()", 1, 5000)
	if err != nil {
		return "", err
	}
	if len(resp.Rows) > 0 && len(resp.Rows[0]) > 0 && resp.Rows[0][0] != nil {
		version = *resp.Rows[0][0]
	}
	return version, nil
}

// ListDatabases and ListTables answer the two questions a console cannot be
// used without: which schemas exist, and what is in the one I picked. Both go
// through the same guarded path as any other statement.
func (s *DatabaseQueryService) ListDatabases(ctx context.Context, p Principal, instanceID int64) ([]string, error) {
	return s.singleColumn(ctx, p, instanceID, "", "SHOW DATABASES")
}

func (s *DatabaseQueryService) ListTables(ctx context.Context, p Principal, instanceID int64, database string) ([]string, error) {
	if strings.TrimSpace(database) == "" {
		return nil, errors.New("database is required")
	}
	return s.singleColumn(ctx, p, instanceID, database, "SHOW TABLES")
}

func (s *DatabaseQueryService) singleColumn(ctx context.Context, p Principal, instanceID int64, database, stmt string) ([]string, error) {
	inst, err := s.instances.Get(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if err := AuthorizeQuery(p, inst); err != nil {
		return nil, err
	}
	if database == "" {
		database = inst.DefaultDatabase
	}
	resp, err := s.execute(ctx, instanceID, database, stmt, contract.NodeDBMaxRows, 10000)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(resp.Rows))
	for _, row := range resp.Rows {
		if len(row) > 0 && row[0] != nil {
			out = append(out, *row[0])
		}
	}
	return out, nil
}

// QueryLogView is an audit entry as the API returns it. Result carries the
// decoded snapshot and is populated only for a single-entry read — a listing
// would otherwise ship every past result set at once.
type QueryLogView struct {
	*domain.DBQueryLog
	Result *QuerySnapshot `json:"result,omitempty"`
}

// QuerySnapshot is the stored shape of a past result set.
type QuerySnapshot struct {
	Columns   []contract.NodeDBColumn `json:"columns"`
	Rows      [][]*string             `json:"rows"`
	Truncated bool                    `json:"truncated"`
}

// ListLogs returns audit entries visible to p. A non-admin sees only their own,
// enforced by narrowing the query itself rather than by filtering afterwards.
func (s *DatabaseQueryService) ListLogs(ctx context.Context, p Principal, instanceID int64, limit, offset int) ([]*domain.DBQueryLog, int64, error) {
	filter := repository.DBQueryLogFilter{InstanceID: instanceID, Limit: limit, Offset: offset}
	if !p.IsAdmin {
		userID := p.UserID
		filter.UserID = &userID
	}
	items, err := s.auditLogs.List(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.auditLogs.Count(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// GetLog returns one audit entry with its stored result decoded.
func (s *DatabaseQueryService) GetLog(ctx context.Context, p Principal, id int64) (*QueryLogView, error) {
	entry, err := s.auditLogs.GetByID(ctx, id)
	if err != nil {
		return nil, ErrAuditEntryNotFound
	}
	if !p.IsAdmin && entry.UserID != p.UserID {
		return nil, ErrAuditEntryNotFound
	}
	view := &QueryLogView{DBQueryLog: entry}
	if entry.ResultSnapshot != "" {
		snapshot, err := s.decodeSnapshot(entry.ResultSnapshot)
		if err != nil {
			// A snapshot that will not decode (rotated master key, corrupt row)
			// must not hide the rest of the audit entry, which is the part that
			// matters most.
			logger.L().Warn("failed to decode query log snapshot", zap.Int64("id", id), zap.Error(err))
		} else {
			view.Result = snapshot
		}
	}
	return view, nil
}

// PurgeExpiredLogs deletes audit entries past the retention window and returns
// how many were removed.
func (s *DatabaseQueryService) PurgeExpiredLogs(ctx context.Context) (int64, error) {
	days := defaultQueryLogRetentionDays
	if raw, ok := s.settings.Get(SettingQueryLogRetentionDays); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n > 0 {
			days = n
		}
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	return s.auditLogs.DeleteOlderThan(ctx, cutoff)
}

// encodeSnapshot renders a capped copy of the result set and encrypts it. The
// caps are applied before encoding, so an oversized result never becomes an
// oversized row.
func (s *DatabaseQueryService) encodeSnapshot(resp *contract.NodeDBQueryResponse) (blob string, truncated bool) {
	snapshot := QuerySnapshot{Columns: resp.Columns, Truncated: resp.Truncated}
	rows := resp.Rows
	if len(rows) > snapshotMaxRows {
		rows = rows[:snapshotMaxRows]
		snapshot.Truncated = true
	}
	snapshot.Rows = rows

	raw, err := sonic.MarshalString(snapshot)
	if err != nil {
		logger.L().Warn("failed to encode query log snapshot", zap.Error(err))
		return "", snapshot.Truncated
	}
	if len(raw) > snapshotMaxBytes {
		// Too large even after the row cap (very wide rows). Keep the shape of
		// the result so the entry still says what was returned, and drop the
		// data rather than storing a blob that would have to be cut mid-JSON.
		snapshot.Rows = nil
		snapshot.Truncated = true
		raw, err = sonic.MarshalString(snapshot)
		if err != nil {
			return "", true
		}
	}

	if !s.box.Enabled() {
		logger.L().Warn("storing query results without encryption; set security.master_key to encrypt them at rest")
	}
	enc, err := s.box.Encrypt(raw)
	if err != nil {
		logger.L().Warn("failed to encrypt query log snapshot", zap.Error(err))
		return "", snapshot.Truncated
	}
	return enc, snapshot.Truncated
}

func (s *DatabaseQueryService) decodeSnapshot(blob string) (*QuerySnapshot, error) {
	raw, err := s.box.Decrypt(blob)
	if err != nil {
		return nil, err
	}
	var snapshot QuerySnapshot
	if err := sonic.UnmarshalString(raw, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// recordAudit writes the trail entry. A failure here is logged but does not
// fail the query: the statement has already run, and refusing to return a
// result the database already produced would not un-run it.
func (s *DatabaseQueryService) recordAudit(ctx context.Context, entry *domain.DBQueryLog) {
	if err := s.auditLogs.Create(ctx, entry); err != nil {
		logger.L().Error("failed to record database query audit entry",
			zap.Int64("instance_id", entry.InstanceID),
			zap.Int64("user_id", entry.UserID),
			zap.Error(err),
		)
	}
}

func clampInt(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

// truncateString cuts s to at most max bytes without splitting a rune — a
// half-encoded character would make the stored statement invalid UTF-8.
func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
