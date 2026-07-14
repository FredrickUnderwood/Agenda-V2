package node

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
)

func writeLogFile(t *testing.T, dir, name string, lines int) {
	t.Helper()
	var sb strings.Builder
	for i := 1; i <= lines; i++ {
		sb.WriteString(fmt.Sprintf(`{"msg":"line %d"}`+"\n", i))
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestFindLogFiles_MatchesAppInstancePrefix(t *testing.T) {
	dir := t.TempDir()
	writeLogFile(t, dir, "myapp__default.log", 1)
	writeLogFile(t, dir, "myapp__default__worker.log", 1)
	writeLogFile(t, dir, "myapp__other.log", 1)
	writeLogFile(t, dir, "otherapp__default.log", 1)

	got, err := findLogFiles(dir, "myapp", "default")
	if err != nil {
		t.Fatalf("findLogFiles: %v", err)
	}
	want := []string{"myapp__default.log", "myapp__default__worker.log"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestServiceNameFromFile(t *testing.T) {
	cases := []struct{ file, app, instance, want string }{
		{"myapp__default.log", "myapp", "default", ""},
		{"myapp__default__worker.log", "myapp", "default", "worker"},
	}
	for _, c := range cases {
		if got := serviceNameFromFile(c.file, c.app, c.instance); got != c.want {
			t.Errorf("serviceNameFromFile(%q) = %q, want %q", c.file, got, c.want)
		}
	}
}

func TestTailLines_ReturnsLastN(t *testing.T) {
	dir := t.TempDir()
	writeLogFile(t, dir, "app.log", 10)
	lines, err := tailLines(filepath.Join(dir, "app.log"), 3)
	if err != nil {
		t.Fatalf("tailLines: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %v", len(lines), lines)
	}
	want := []string{`{"msg":"line 8"}`, `{"msg":"line 9"}`, `{"msg":"line 10"}`}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("lines[%d] = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestServer_GetLogs_SingleFile(t *testing.T) {
	dir := t.TempDir()
	writeLogFile(t, dir, "myapp__default.log", 5)

	s := NewServer("tok", NewJobStore(1024, 0), NewProxyRegistry(), "")
	req := httptest.NewRequest(http.MethodGet, "/v1/logs/myapp/default?dir="+dir+"&tail=2", nil)
	req.Header.Set(contract.HeaderNodeToken, "tok")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp contract.NodeLogsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.App != "myapp" || resp.Instance != "default" {
		t.Fatalf("resp = %+v", resp)
	}
	if len(resp.Logs) != 1 || len(resp.Logs[0].Lines) != 2 {
		t.Fatalf("resp.Logs = %+v", resp.Logs)
	}
}

func TestServer_GetLogs_MultiServiceFilteredByServiceParam(t *testing.T) {
	dir := t.TempDir()
	writeLogFile(t, dir, "myapp__default__api.log", 1)
	writeLogFile(t, dir, "myapp__default__worker.log", 1)

	s := NewServer("tok", NewJobStore(1024, 0), NewProxyRegistry(), "")
	req := httptest.NewRequest(http.MethodGet, "/v1/logs/myapp/default?dir="+dir+"&service=worker", nil)
	req.Header.Set(contract.HeaderNodeToken, "tok")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp contract.NodeLogsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Logs) != 1 || resp.Logs[0].Service != "worker" {
		t.Fatalf("resp.Logs = %+v", resp.Logs)
	}
}

func TestServer_GetLogs_MissingDirParam(t *testing.T) {
	s := NewServer("tok", NewJobStore(1024, 0), NewProxyRegistry(), "")
	req := httptest.NewRequest(http.MethodGet, "/v1/logs/myapp/default", nil)
	req.Header.Set(contract.HeaderNodeToken, "tok")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

func TestServer_GetLogs_NotFound(t *testing.T) {
	dir := t.TempDir()
	s := NewServer("tok", NewJobStore(1024, 0), NewProxyRegistry(), "")
	req := httptest.NewRequest(http.MethodGet, "/v1/logs/nope/default?dir="+dir, nil)
	req.Header.Set(contract.HeaderNodeToken, "tok")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}
}

func TestServer_GetLogs_RequiresToken(t *testing.T) {
	dir := t.TempDir()
	writeLogFile(t, dir, "myapp__default.log", 1)
	s := NewServer("tok", NewJobStore(1024, 0), NewProxyRegistry(), "")
	req := httptest.NewRequest(http.MethodGet, "/v1/logs/myapp/default?dir="+dir, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body = %s", rec.Code, rec.Body.String())
	}
}
