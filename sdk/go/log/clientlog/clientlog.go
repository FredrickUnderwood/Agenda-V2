// Package clientlog is the server-side ingest for browser/client logs shipped by
// the agenda TS SDK (@agenda/log, browser entry).
//
// A browser cannot write the /var/log/agenda/<app>__<instance>__<service>.log
// files that agenda-node tails, so the browser SDK batches its logs and POSTs
// them to an endpoint the app mounts with Handler. Each entry is re-emitted
// through sdk/go/log, so it lands in that backend's own rotating log file with
// the backend's app/service/env/instance identity — and, when the handler is
// mounted behind ginlog.Middleware (so the request context carries the trace
// id), with the same trace_id as the request that carried it. The result: client
// logs appear in the console's Logs tab under the backend app, correlated to the
// request, without any new platform ingest surface.
//
// The handler is a plain http.Handler with no gin/framework dependency; mount it
// directly (net/http mux) or via gin.WrapH:
//
//	r.POST("/api/client-logs", gin.WrapH(clientlog.Handler(clientlog.Options{})))
//
// Because it re-emits at the client-supplied level, the process logger's own
// level filter applies (a client "debug" is dropped when the backend runs at
// info), and every server-side cardinality/size guard still holds: the endpoint
// is public-facing, so Handler caps body size, batch length, message length and
// serialized field size, and nests all client-supplied fields under a single
// "client" object so they can never overwrite the line's identity fields.
package clientlog

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/sdk/go/log"
)

// Default guard limits. The endpoint is reachable by every browser running the
// app, so the defaults are deliberately conservative; raise them via Options
// only when a real payload needs it.
const (
	defaultMaxBatch       = 100
	defaultMaxMessageLen  = 4096
	defaultMaxFieldsBytes = 8192
	defaultMaxBodyBytes   = 256 << 10 // 256 KiB
	defaultSource         = "client"
)

// Options tunes the ingest handler. The zero value is valid and uses the
// package defaults above.
type Options struct {
	// MaxBatch caps how many entries a single POST may contribute; extras are
	// silently dropped (the browser SDK already batches to a small size).
	MaxBatch int
	// MaxMessageLen truncates each entry's msg to this many bytes.
	MaxMessageLen int
	// MaxFieldsBytes drops an entry's fields object when its JSON serialization
	// exceeds this many bytes (the entry is still logged, without fields).
	MaxFieldsBytes int
	// MaxBodyBytes caps the request body; a larger body is rejected with 400.
	MaxBodyBytes int64
	// Source is the value of the "source" field stamped on every ingested line
	// so client logs are distinguishable from the backend's own. Default
	// "client".
	Source string
}

func (o Options) withDefaults() Options {
	if o.MaxBatch <= 0 {
		o.MaxBatch = defaultMaxBatch
	}
	if o.MaxMessageLen <= 0 {
		o.MaxMessageLen = defaultMaxMessageLen
	}
	if o.MaxFieldsBytes <= 0 {
		o.MaxFieldsBytes = defaultMaxFieldsBytes
	}
	if o.MaxBodyBytes <= 0 {
		o.MaxBodyBytes = defaultMaxBodyBytes
	}
	if o.Source == "" {
		o.Source = defaultSource
	}
	return o
}

// wireEntry is one browser log line as sent by @agenda/log. All fields are
// optional; a missing/unknown level is treated as info.
type wireEntry struct {
	Level  string         `json:"level"`
	Msg    string         `json:"msg"`
	TS     string         `json:"ts"`     // client-side ISO timestamp (informational)
	Logger string         `json:"logger"` // originating source, e.g. "console"/"window.onerror"/"react"
	Fields map[string]any `json:"fields"`
}

// wirePayload is the POST body shape: {"logs":[ ... ]}.
type wirePayload struct {
	Logs []wireEntry `json:"logs"`
}

// Handler returns an http.Handler that ingests a batch of browser logs and
// re-emits each through sdk/go/log. It accepts only POST and always answers
// 204 on a well-formed body (so the browser's fire-and-forget transport never
// blocks on a response), or 400 on a malformed/oversize body.
func Handler(opts Options) http.Handler {
	o := opts.withDefaults()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, o.MaxBodyBytes)
		var p wirePayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, "invalid client log payload", http.StatusBadRequest)
			return
		}
		ctx := r.Context()
		n := len(p.Logs)
		if n > o.MaxBatch {
			n = o.MaxBatch
		}
		for i := 0; i < n; i++ {
			emit(ctx, o, p.Logs[i])
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// emit re-logs one client entry through sdk/go/log at its (normalized) level,
// carrying ctx so the trace_id (when the handler is mounted behind
// ginlog.Middleware) is attached automatically.
func emit(ctx context.Context, o Options, e wireEntry) {
	msg := truncate(strings.TrimSpace(e.Msg), o.MaxMessageLen)
	if msg == "" {
		msg = "(empty client log)"
	}

	fields := make([]zap.Field, 0, 4)
	fields = append(fields, zap.String("source", o.Source))
	if e.Logger != "" {
		fields = append(fields, zap.String("client_logger", truncate(e.Logger, 128)))
	}
	if e.TS != "" {
		fields = append(fields, zap.String("client_ts", truncate(e.TS, 64)))
	}
	if len(e.Fields) > 0 {
		if b, err := json.Marshal(e.Fields); err == nil && len(b) <= o.MaxFieldsBytes {
			fields = append(fields, zap.Any("client", e.Fields))
		} else {
			fields = append(fields, zap.String("client_fields", "dropped: oversize or unserializable"))
		}
	}

	switch normalizeLevel(e.Level) {
	case "debug":
		log.Debug(ctx, msg, fields...)
	case "warn":
		log.Warn(ctx, msg, fields...)
	case "error":
		log.Error(ctx, msg, fields...)
	default:
		log.Info(ctx, msg, fields...)
	}
}

// normalizeLevel maps a client level to one of debug/info/warn/error, folding
// common aliases and defaulting unknown values to info.
func normalizeLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug", "trace":
		return "debug"
	case "warn", "warning":
		return "warn"
	case "error", "fatal", "critical":
		return "error"
	default:
		return "info"
	}
}

// truncate clips s to at most max bytes (on a rune boundary) so a hostile or
// buggy client can't blow up a log line.
func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	// Back off to a rune boundary so we don't split a multibyte character.
	cut := max
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	if cut == 0 {
		cut = max
	}
	return s[:cut] + "…"
}

// utf8RuneStart reports whether b is the first byte of a UTF-8 rune (i.e. not a
// 10xxxxxx continuation byte).
func utf8RuneStart(b byte) bool {
	return b&0xC0 != 0x80
}
