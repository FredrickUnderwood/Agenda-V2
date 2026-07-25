package log

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestInit_AttachesIdentityFields(t *testing.T) {
	dir := t.TempDir()
	if err := Init(Config{AppName: "svc", ServiceName: "worker", Env: "prod", InstanceName: "blue", LogDir: dir}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	Info(context.Background(), "hello")
	Shutdown()

	lines := readLines(t, filepath.Join(dir, "svc__blue__worker.log"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	for k, want := range map[string]string{"app": "svc", "service": "worker", "env": "prod", "instance": "blue"} {
		if lines[0][k] != want {
			t.Errorf("field %q = %v, want %q", k, lines[0][k], want)
		}
	}
}

func TestInit_OmitsUnsetIdentityFields(t *testing.T) {
	dir := t.TempDir()
	if err := Init(Config{AppName: "svc", LogDir: dir}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	Info(context.Background(), "hello")
	Shutdown()

	m := readLines(t, filepath.Join(dir, "svc.log"))[0]
	for _, k := range []string{"env", "service", "instance"} {
		if _, ok := m[k]; ok {
			t.Errorf("field %q should be absent when unset, got %v", k, m[k])
		}
	}
}

func TestInfo_EmitsTraceIDFromContext(t *testing.T) {
	dir := t.TempDir()
	if err := Init(Config{AppName: "svc", LogDir: dir}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	Info(ContextWithTraceID(context.Background(), "abc123"), "with-trace")
	Info(context.Background(), "no-trace")
	Shutdown()

	lines := readLines(t, filepath.Join(dir, "svc.log"))
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[0]["trace_id"] != "abc123" {
		t.Errorf("line 0 trace_id = %v, want abc123", lines[0]["trace_id"])
	}
	if _, ok := lines[1]["trace_id"]; ok {
		t.Errorf("line 1 should have no trace_id, got %v", lines[1]["trace_id"])
	}
}

func TestContextWithTraceID(t *testing.T) {
	if got := TraceIDFromContext(ContextWithTraceID(context.Background(), "x")); got != "x" {
		t.Errorf("round trip = %q, want x", got)
	}
	if got := TraceIDFromContext(ContextWithTraceID(context.Background(), "")); got != "" {
		t.Errorf("empty id should not be stored, got %q", got)
	}
	if got := TraceIDFromContext(context.Background()); got != "" {
		t.Errorf("no id present → want empty, got %q", got)
	}
}

func TestNewTraceID(t *testing.T) {
	a, b := NewTraceID(), NewTraceID()
	if len(a) != 32 {
		t.Errorf("trace id len = %d, want 32 hex chars", len(a))
	}
	if a == b {
		t.Errorf("two trace ids collided: %q", a)
	}
}

func TestTraceIDFromRequest(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if TraceIDFromRequest(r) != "" {
		t.Error("expected empty for header-less request")
	}
	r.Header.Set(TraceHeader, "tid")
	if got := TraceIDFromRequest(r); got != "tid" {
		t.Errorf("= %q, want tid", got)
	}
}

// captureRT records the trace header of the request it receives.
type captureRT struct{ seen string }

func (c *captureRT) RoundTrip(req *http.Request) (*http.Response, error) {
	c.seen = req.Header.Get(TraceHeader)
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
}

func TestTransport_InjectsContextTraceID(t *testing.T) {
	cap := &captureRT{}
	ctx := ContextWithTraceID(context.Background(), "tid-1")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://x/", nil)
	if _, err := NewTransport(cap).RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if cap.seen != "tid-1" {
		t.Errorf("outgoing trace header = %q, want tid-1", cap.seen)
	}
	// Per the RoundTripper contract the caller's request must not be mutated.
	if req.Header.Get(TraceHeader) != "" {
		t.Errorf("caller request was mutated: %q", req.Header.Get(TraceHeader))
	}
}

func TestTransport_DoesNotOverrideExistingHeader(t *testing.T) {
	cap := &captureRT{}
	ctx := ContextWithTraceID(context.Background(), "from-ctx")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://x/", nil)
	req.Header.Set(TraceHeader, "explicit")
	if _, err := NewTransport(cap).RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if cap.seen != "explicit" {
		t.Errorf("should keep explicit header, got %q", cap.seen)
	}
}

func TestTransport_NoContextID_NoHeader(t *testing.T) {
	cap := &captureRT{}
	req, _ := http.NewRequest(http.MethodGet, "http://x/", nil)
	if _, err := NewTransport(cap).RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if cap.seen != "" {
		t.Errorf("no ctx id → no header, got %q", cap.seen)
	}
}
