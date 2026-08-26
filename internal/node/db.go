package node

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/sqlguard"
	"github.com/FredrickUnderwood/agenda-v2/sdk/go/log"
)

// dbDialTimeout bounds establishing the connection, separately from the
// statement timeout: a database that is down should fail fast rather than burn
// the caller's whole query budget on a TCP handshake that will never complete.
const dbDialTimeout = 5 * time.Second

// runLocalQuery opens a connection to <backendHost>:<port> — a database on
// this machine, reachable only from here — runs one already-validated
// read-only statement, and returns the result set.
//
// backendHost mirrors fetchLocalMetrics's and ProxyHandler's: under
// docker-outside-of-docker the database's published port lives on the Docker
// host rather than inside the node's own container, so it defaults to
// 127.0.0.1 but is configurable (host.docker.internal under DooD). That also
// means the connection reaches the database from the bridge address, not
// loopback — the registered account must be allowed from there.
//
// A connection is opened per query and closed afterwards. That costs one
// handshake (single-digit milliseconds over loopback) and buys statelessness:
// the session settings below can never leak into somebody else's next query.
func runLocalQuery(ctx context.Context, backendHost string, req contract.NodeDBQueryRequest) (*contract.NodeDBQueryResponse, error) {
	stmt, err := sqlguard.EnsureReadOnly(req.SQL)
	if err != nil {
		return nil, err
	}
	if backendHost == "" {
		backendHost = "127.0.0.1"
	}

	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(backendHost, strconv.Itoa(req.Port))
	cfg.User = req.User
	cfg.Passwd = req.Password
	cfg.DBName = req.Database
	cfg.Timeout = dbDialTimeout
	cfg.ReadTimeout = time.Duration(req.TimeoutMS)*time.Millisecond + dbDialTimeout
	cfg.WriteTimeout = dbDialTimeout
	cfg.AllowNativePasswords = true
	// Left at the driver's defaults, but stated explicitly because the read-only
	// guarantee depends on them: without multi-statement support, a semicolon
	// that slipped past sqlguard still cannot start a second statement.
	cfg.MultiStatements = false
	cfg.InterpolateParams = false

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	queryCtx, cancel := context.WithTimeout(ctx, time.Duration(req.TimeoutMS)*time.Millisecond)
	defer cancel()

	// Take one pinned connection: the session settings below only protect the
	// statement if it runs on the same connection they were set on.
	conn, err := db.Conn(queryCtx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	applyReadOnlySession(queryCtx, conn, req.TimeoutMS)

	started := time.Now()
	rows, err := conn.QueryContext(queryCtx, stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	resp, err := scanRows(rows, req.MaxRows, req.MaxBytes)
	if err != nil {
		return nil, err
	}
	resp.DurationMS = int(time.Since(started).Milliseconds())
	return resp, nil
}

// applyReadOnlySession asks the server to enforce read-only and a statement
// deadline itself, so neither depends on this process behaving correctly. Both
// are best-effort: the variable names differ across server versions and
// MariaDB, and an older server that has neither still runs behind sqlguard and
// (the real boundary) an account with only SELECT granted. A failure is logged
// rather than fatal, because refusing to run a plain SELECT against an older
// server would be a worse outcome than running it with one fewer backstop.
func applyReadOnlySession(ctx context.Context, conn *sql.Conn, timeoutMS int) {
	readOnly := []string{
		"SET SESSION transaction_read_only = ON", // MySQL 5.7.20+
		"SET SESSION tx_read_only = ON",          // MySQL 5.6 / early 5.7
	}
	if !execFirstThatWorks(ctx, conn, readOnly) {
		log.L().Warn("could not set a read-only session on the target database; " +
			"relying on the statement guard and the account's own privileges")
	}

	deadline := []string{
		fmt.Sprintf("SET SESSION max_execution_time = %d", timeoutMS),                 // MySQL 5.7+
		fmt.Sprintf("SET SESSION max_statement_time = %f", float64(timeoutMS)/1000.0), // MariaDB
	}
	if !execFirstThatWorks(ctx, conn, deadline) {
		log.L().Warn("could not set a server-side statement timeout on the target database",
			zap.Int("timeout_ms", timeoutMS))
	}
}

func execFirstThatWorks(ctx context.Context, conn *sql.Conn, stmts []string) bool {
	for _, s := range stmts {
		if _, err := conn.ExecContext(ctx, s); err == nil {
			return true
		}
	}
	return false
}

// scanRows materializes the result set, stopping as soon as either cap is hit.
// The caps are enforced while scanning rather than after: a query returning a
// million rows must never be fully read into memory first and trimmed
// afterwards, or the cap protects nothing.
func scanRows(rows *sql.Rows, maxRows, maxBytes int) (*contract.NodeDBQueryResponse, error) {
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}

	cols := make([]contract.NodeDBColumn, len(names))
	for i, n := range names {
		cols[i] = contract.NodeDBColumn{Name: n}
		if i < len(types) {
			cols[i].Type = types[i].DatabaseTypeName()
		}
	}

	resp := &contract.NodeDBQueryResponse{Columns: cols, Rows: make([][]*string, 0, 64)}
	raw := make([][]byte, len(names))
	holders := make([]any, len(names))
	for i := range holders {
		holders[i] = &raw[i]
	}

	bytesUsed := 0
	for rows.Next() {
		if len(resp.Rows) >= maxRows {
			resp.Truncated = true
			break
		}
		if err := rows.Scan(holders...); err != nil {
			return nil, err
		}
		row := make([]*string, len(names))
		for i, b := range raw {
			if b == nil {
				continue // SQL NULL stays a nil pointer, distinct from ""
			}
			var cell string
			if utf8.Valid(b) {
				cell = string(b)
			} else {
				// Non-text bytes cannot ride in JSON as-is; encode them and
				// flag the column so the console can label what it is showing.
				cell = base64.StdEncoding.EncodeToString(b)
				cols[i].Binary = true
			}
			bytesUsed += len(cell)
			row[i] = &cell
		}
		resp.Rows = append(resp.Rows, row)
		if bytesUsed >= maxBytes {
			resp.Truncated = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	resp.Columns = cols
	resp.RowCount = len(resp.Rows)
	return resp, nil
}

// clampDBQueryRequest applies the node's own defaults and ceilings. The control
// plane clamps too, but the node cannot take that on trust: this endpoint is
// reachable by anything holding the node token.
func clampDBQueryRequest(req *contract.NodeDBQueryRequest) error {
	if req.Driver == "" {
		req.Driver = contract.NodeDBDriverMySQL
	}
	if req.Driver != contract.NodeDBDriverMySQL {
		return errors.New("unsupported driver: " + req.Driver)
	}
	if req.Port <= 0 || req.Port > 65535 {
		return errors.New("port must be a valid TCP port")
	}
	if req.User == "" {
		return errors.New("user is required")
	}
	if req.MaxRows <= 0 {
		req.MaxRows = contract.NodeDBDefaultMaxRows
	}
	if req.MaxRows > contract.NodeDBMaxRows {
		req.MaxRows = contract.NodeDBMaxRows
	}
	if req.MaxBytes <= 0 {
		req.MaxBytes = contract.NodeDBDefaultMaxBytes
	}
	if req.MaxBytes > contract.NodeDBMaxBytes {
		req.MaxBytes = contract.NodeDBMaxBytes
	}
	if req.TimeoutMS <= 0 {
		req.TimeoutMS = contract.NodeDBDefaultTimeoutMS
	}
	if req.TimeoutMS > contract.NodeDBMaxTimeoutMS {
		req.TimeoutMS = contract.NodeDBMaxTimeoutMS
	}
	return nil
}
