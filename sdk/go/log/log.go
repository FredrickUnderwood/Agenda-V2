// Package log is the agenda first-party structured-logging helper. It is a thin
// zap wrapper that both the platform's own services and external Go projects can
// use to emit logs in a consistent shape. It lives in the standalone module
// github.com/FredrickUnderwood/agenda-v2/sdk/go so importing it does not pull in
// the platform's dependencies.
package log

import (
	"context"
	"sync"

	"go.uber.org/zap"
)

// Config configures the process logger. AppName, when set, is attached as a
// field to every log line so multi-service log streams stay attributable.
type Config struct {
	AppName string
	Level   string
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
	if cfg.AppName != "" {
		l = l.With(zap.String("app", cfg.AppName))
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
