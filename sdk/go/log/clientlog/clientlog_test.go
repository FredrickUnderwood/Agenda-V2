package clientlog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/sdk/go/log"
)

// initFileLog points sdk/go/log at a temp file so the test can read back the
// lines the handler emits, and returns that file's path.
func initFileLog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := log.Init(log.Config{AppName: "web", InstanceName: "default", ServiceName: "api", LogDir: dir, Level: "debug"}); err != nil {
		t.Fatalf("log.Init: %v", err)
	}
	return filepath.Join(dir, "web__default__api.log")
}

func readLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func post(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/client-logs", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHandler_EmitsBatchThroughLog(t *testing.T) {
	path := initFileLog(t)
	h := Handler(Options{})

	rr := post(t, h, `{"logs":[
		{"level":"error","msg":"boom","logger":"window.onerror","ts":"2026-07-30T00:00:00.000Z","fields":{"url":"https://app/x","stack":"at f"}},
		{"level":"info","msg":"page view"}
	]}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %q)", rr.Code, rr.Body.String())
	}
	log.Shutdown()

	lines := readLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %v", len(lines), lines)
	}

	first := lines[0]
	if first["level"] != "error" {
		t.Errorf("level = %v, want error", first["level"])
	}
	if first["msg"] != "boom" {
		t.Errorf("msg = %v, want boom", first["msg"])
	}
	if first["source"] != "client" {
		t.Errorf("source = %v, want client", first["source"])
	}
	if first["client_logger"] != "window.onerror" {
		t.Errorf("client_logger = %v, want window.onerror", first["client_logger"])
	}
	// Identity is the backend's own, not anything the browser could spoof.
	if first["app"] != "web" || first["service"] != "api" {
		t.Errorf("identity = app:%v service:%v, want web/api", first["app"], first["service"])
	}
	// Client-supplied fields are nested under "client", never top-level, so they
	// can't shadow identity fields.
	client, ok := first["client"].(map[string]any)
	if !ok {
		t.Fatalf("client field = %v, want nested object", first["client"])
	}
	if client["url"] != "https://app/x" {
		t.Errorf("client.url = %v, want https://app/x", client["url"])
	}
}

func TestHandler_RejectsNonPost(t *testing.T) {
	h := Handler(Options{})
	req := httptest.NewRequest(http.MethodGet, "/api/client-logs", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}

func TestHandler_BadJSON(t *testing.T) {
	h := Handler(Options{})
	rr := post(t, h, `{"logs": not json`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestHandler_CapsBatch(t *testing.T) {
	path := initFileLog(t)
	h := Handler(Options{MaxBatch: 2})

	rr := post(t, h, `{"logs":[
		{"msg":"a"},{"msg":"b"},{"msg":"c"},{"msg":"d"}
	]}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rr.Code)
	}
	log.Shutdown()

	lines := readLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 (batch capped)", len(lines))
	}
}

func TestHandler_UnknownLevelBecomesInfo_AndEmptyMsgPlaceholder(t *testing.T) {
	path := initFileLog(t)
	h := Handler(Options{})

	rr := post(t, h, `{"logs":[{"level":"weird","msg":"   "}]}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rr.Code)
	}
	log.Shutdown()

	lines := readLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	if lines[0]["level"] != "info" {
		t.Errorf("level = %v, want info", lines[0]["level"])
	}
	if lines[0]["msg"] != "(empty client log)" {
		t.Errorf("msg = %v, want placeholder", lines[0]["msg"])
	}
}

func TestHandler_OversizeFieldsDropped(t *testing.T) {
	path := initFileLog(t)
	h := Handler(Options{MaxFieldsBytes: 32})

	big := strings.Repeat("x", 200)
	rr := post(t, h, `{"logs":[{"msg":"m","fields":{"blob":"`+big+`"}}]}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rr.Code)
	}
	log.Shutdown()

	lines := readLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	if _, present := lines[0]["client"]; present {
		t.Errorf("oversize fields should be dropped, got client=%v", lines[0]["client"])
	}
	if lines[0]["client_fields"] == nil {
		t.Errorf("expected a client_fields drop marker")
	}
}
