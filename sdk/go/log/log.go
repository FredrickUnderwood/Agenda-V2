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
type Config struct {
	AppName      string
	Level        string
	LogDir       string
	InstanceName string
	ServiceName  string
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
