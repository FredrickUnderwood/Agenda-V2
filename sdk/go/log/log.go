// Package log is the agenda first-party structured-logging helper. It is a thin
// zap wrapper that both the platform's own services and external Go projects can
// use to emit logs in a consistent shape. It lives in the standalone module
// github.com/FredrickUnderwood/agenda-v2/sdk/go so importing it does not pull in
// the platform's dependencies.
package log

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Config configures the process logger. AppName, when set, is attached as a
// field to every log line so multi-service log streams stay attributable.
//
// LogDir, when set (or via the AGENDA_LOG_DIR env var the platform injects
// into deployed app containers), adds a rotating JSON-lines file sink at
// <LogDir>/<AppName>[__<InstanceName>][__<ServiceName>].log alongside
// stderr. Leaving both unset keeps today's stderr-only behavior unchanged.
//
// InstanceName and ServiceName disambiguate the log file when one mounted
// log directory is shared by more than one deploy target (InstanceName,
// e.g. a blue/green slot — env AGENDA_INSTANCE_NAME) and/or more than one
// container of a multi-service compose app (ServiceName — env
// AGENDA_SERVICE_NAME). Both are platform-injected; most single-service,
// single-instance apps will only ever see LogDir/AppName populated.
//
// ReplicaID disambiguates the log file between replicas of the SAME service
// (e.g. `docker compose up --scale api=3` or a `deploy.replicas` block).
// Without it, every replica opens the same <App>__<Instance>__<Service>.log
// and their independent lumberjack rotations race and corrupt each other. It
// is deliberately opt-in and unset by default: leaving it empty preserves the
// single-replica filename exactly, so redeploys keep writing to one stable
// file rather than churning a new one each time.
//
// Scaled services enable it one of two ways (see Init): set ReplicaID / the
// AGENDA_REPLICA_ID env to an explicit per-container-unique value, or just set
// the boolean AGENDA_LOG_PER_REPLICA=1 and let the SDK fall back to the
// container hostname automatically (no ${HOSTNAME} interpolation to get wrong).
type Config struct {
	AppName      string
	Level        string
	LogDir       string
	InstanceName string
	ServiceName  string
	ReplicaID    string
}

// firstNonEmpty returns the first non-empty value, letting an explicit Config
// field win over the platform-injected env var of the same name.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// isTruthy reports whether an env var value asks for an opt-in flag to be on.
func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

var (
	mu     sync.RWMutex
	logger *zap.Logger

	// ContextFields, when set, extracts additional fields from the request
	// context (e.g. a trace or request id) for the context-aware helpers. It is
	// a hook so callers can enrich logs without this package importing their
	// context keys.
	ContextFields func(ctx context.Context) []zap.Field
)

// Init builds the process logger from cfg. Safe to call again to reconfigure.
func Init(cfg Config) error {
	appName := firstNonEmpty(cfg.AppName, os.Getenv("AGENDA_APP_NAME"))
	logDir := firstNonEmpty(cfg.LogDir, os.Getenv("AGENDA_LOG_DIR"))
	instanceName := firstNonEmpty(cfg.InstanceName, os.Getenv("AGENDA_INSTANCE_NAME"))
	serviceName := firstNonEmpty(cfg.ServiceName, os.Getenv("AGENDA_SERVICE_NAME"))
	replicaID := firstNonEmpty(cfg.ReplicaID, os.Getenv("AGENDA_REPLICA_ID"))
	// When no explicit replica id is given but per-replica logging is opted
	// into, derive one from the container hostname (unique per replica under
	// docker compose). Kept off by default so single-replica apps keep one
	// stable log filename across redeploys instead of churning a new one.
	if replicaID == "" && isTruthy(os.Getenv("AGENDA_LOG_PER_REPLICA")) {
		if h, err := os.Hostname(); err == nil {
			replicaID = h
		}
	}

	zcfg := zap.NewProductionConfig()
	if cfg.Level != "" {
		var level zap.AtomicLevel
		if err := level.UnmarshalText([]byte(cfg.Level)); err == nil {
			zcfg.Level = level
		}
	}
	l, err := zcfg.Build()
	if err != nil {
		return err
	}
	if logDir != "" {
		fileName := appName
		if fileName == "" {
			fileName = "app"
		}
		if instanceName != "" {
			fileName += "__" + instanceName
		}
		if serviceName != "" {
			fileName += "__" + serviceName
		}
		if replicaID != "" {
			fileName += "__" + replicaID
		}
		fileSink := zapcore.AddSync(&lumberjack.Logger{
			Filename:   filepath.Join(logDir, fileName+".log"),
			MaxSize:    100, // megabytes
			MaxBackups: 5,
			MaxAge:     14, // days
			Compress:   true,
		})
		l = l.WithOptions(zap.WrapCore(func(core zapcore.Core) zapcore.Core {
			fileCore := zapcore.NewCore(zapcore.NewJSONEncoder(zcfg.EncoderConfig), fileSink, zcfg.Level)
			return zapcore.NewTee(core, fileCore)
		}))
	}
	if appName != "" {
		l = l.With(zap.String("app", appName))
	}
	mu.Lock()
	logger = l
	mu.Unlock()
	return nil
}

// L returns the process logger, or a no-op logger if Init has not run.
func L() *zap.Logger {
	mu.RLock()
	defer mu.RUnlock()
	if logger == nil {
		return zap.NewNop()
	}
	return logger
}

// Debug logs at debug level, enriched with any context fields.
func Debug(ctx context.Context, msg string, fields ...zap.Field) {
	L().Debug(msg, withContext(ctx, fields)...)
}

// Info logs at info level, enriched with any context fields.
func Info(ctx context.Context, msg string, fields ...zap.Field) {
	L().Info(msg, withContext(ctx, fields)...)
}

// Warn logs at warn level, enriched with any context fields.
func Warn(ctx context.Context, msg string, fields ...zap.Field) {
	L().Warn(msg, withContext(ctx, fields)...)
}

// Error logs at error level, enriched with any context fields.
func Error(ctx context.Context, msg string, fields ...zap.Field) {
	L().Error(msg, withContext(ctx, fields)...)
}

// Shutdown flushes any buffered log entries. Call on process exit.
func Shutdown() {
	_ = L().Sync()
}

func withContext(ctx context.Context, fields []zap.Field) []zap.Field {
	if ctx == nil || ContextFields == nil {
		return fields
	}
	extra := ContextFields(ctx)
	if len(extra) == 0 {
		return fields
	}
	return append(extra, fields...)
}
