package log

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// TraceHeader is the HTTP header the agenda gateway sets on every proxied
// request (generating one when the client didn't supply it) and that services
// propagate on their own outbound calls, so a single request is correlatable
// across the gateway and every service it touches. The gateway keeps a matching
// constant of its own (internal/gateway); both must name the same header.
const TraceHeader = "X-Agenda-Trace-Id"

// traceField is the structured-log field name the trace id is emitted under.
const traceField = "trace_id"

// traceCtxKey is the unexported context key the trace id is stored under, so no
// other package can collide with it.
type traceCtxKey struct{}

// ContextWithTraceID returns ctx carrying id, so the context-aware log helpers
// (Debug/Info/Warn/Error) automatically emit it as trace_id. Returns ctx
// unchanged for an empty id.
func ContextWithTraceID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, traceCtxKey{}, id)
}

// TraceIDFromContext returns the trace id carried by ctx, or "" if none.
func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(traceCtxKey{}).(string)
	return id
}

// TraceIDFromRequest returns the incoming request's trace id header, or "".
func TraceIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Header.Get(TraceHeader)
}

// NewTraceID returns a new random 128-bit trace id as 32 lowercase hex chars.
// Returns "" only if the system CSPRNG fails, which callers treat as "no trace".
func NewTraceID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

// TraceMiddleware wraps next with agenda trace propagation for net/http servers
// (the gin equivalent is ginlog.Middleware). It reuses the incoming
// X-Agenda-Trace-Id header (set by the agenda gateway) or mints one when absent,
// stores it on the request context so log.Info(r.Context(), ...) and friends
// emit trace_id, and echoes it on the response so the caller can correlate:
//
//	handler := log.TraceMiddleware(mux)
//	http.ListenAndServe(addr, handler)
func TraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := TraceIDFromRequest(r)
		if id == "" {
			id = NewTraceID()
		}
		if id != "" {
			w.Header().Set(TraceHeader, id)
			r = r.WithContext(ContextWithTraceID(r.Context(), id))
		}
		next.ServeHTTP(w, r)
	})
}

// Transport is an http.RoundTripper that propagates the caller's trace id: it
// copies the trace id from the request context into the outgoing
// X-Agenda-Trace-Id header (unless the request already carries one), so a
// downstream service reached through the gateway logs under the same trace as
// the caller. Wrap the client used for service-to-service calls:
//
//	client := &http.Client{Transport: log.NewTransport(nil)}
//	// ctx carries the trace id (e.g. from ginlog.Middleware on the inbound request)
//	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
//	resp, _ := client.Do(req)
type Transport struct {
	// Base is the underlying RoundTripper; nil means http.DefaultTransport.
	Base http.RoundTripper
}

// NewTransport wraps base (nil → http.DefaultTransport) with trace propagation.
func NewTransport(base http.RoundTripper) *Transport {
	return &Transport{Base: base}
}

// RoundTrip injects the context trace id into the outgoing request when the
// request doesn't already set one, then delegates to the base transport. Per
// the http.RoundTripper contract it must not modify the caller's request, so it
// clones before setting the header.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if req.Header.Get(TraceHeader) == "" {
		if id := TraceIDFromContext(req.Context()); id != "" {
			req = req.Clone(req.Context())
			req.Header.Set(TraceHeader, id)
		}
	}
	return base.RoundTrip(req)
}
