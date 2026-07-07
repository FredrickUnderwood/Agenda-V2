package log

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func readLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("unmarshal line %q: %v", line, err)
		}
		lines = append(lines, m)
	}
	return lines
}

func TestInit_NoLogDir_NoFileCreated(t *testing.T) {
	dir := t.TempDir()
	if err := Init(Config{AppName: "svc"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	Info(context.Background(), "hello")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no files written outside stderr, got %v", entries)
	}
}

func TestInit_LogDir_WritesFileNamedByAppAndInstance(t *testing.T) {
	dir := t.TempDir()
	if err := Init(Config{AppName: "svc", InstanceName: "api", LogDir: dir}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	Info(context.Background(), "hello", zap.String("k", "v"))
	Shutdown()

	path := filepath.Join(dir, "svc__api.log")
	lines := readLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("expected 1 log line, got %d", len(lines))
	}
	if lines[0]["msg"] != "hello" {
		t.Errorf("msg = %v, want hello", lines[0]["msg"])
	}
	if lines[0]["app"] != "svc" {
		t.Errorf("app field = %v, want svc", lines[0]["app"])
	}
	if lines[0]["k"] != "v" {
		t.Errorf("k field = %v, want v", lines[0]["k"])
	}
}

func TestInit_LogDir_ServiceName_AppendsSuffix(t *testing.T) {
	dir := t.TempDir()
	if err := Init(Config{AppName: "svc", InstanceName: "default", ServiceName: "worker", LogDir: dir}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	Info(context.Background(), "hello")
	Shutdown()

	if _, err := os.Stat(filepath.Join(dir, "svc__default__worker.log")); err != nil {
		t.Fatalf("expected svc__default__worker.log to exist: %v", err)
	}
}

func TestInit_LogDir_NoInstanceName_OmitsSuffix(t *testing.T) {
	dir := t.TempDir()
	if err := Init(Config{AppName: "svc", LogDir: dir}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	Info(context.Background(), "hello")
	Shutdown()

	if _, err := os.Stat(filepath.Join(dir, "svc.log")); err != nil {
		t.Fatalf("expected svc.log to exist: %v", err)
	}
}

func TestInit_EnvVarFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENDA_APP_NAME", "env-app")
	t.Setenv("AGENDA_LOG_DIR", dir)
	t.Setenv("AGENDA_INSTANCE_NAME", "env-instance")

	if err := Init(Config{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	Info(context.Background(), "hello")
	Shutdown()

	if _, err := os.Stat(filepath.Join(dir, "env-app__env-instance.log")); err != nil {
		t.Fatalf("expected env-app__env-instance.log to exist: %v", err)
	}
}

func TestInit_ExplicitConfigWinsOverEnv(t *testing.T) {
	dir := t.TempDir()
	otherDir := t.TempDir()
	t.Setenv("AGENDA_APP_NAME", "env-app")
	t.Setenv("AGENDA_LOG_DIR", otherDir)
	t.Setenv("AGENDA_INSTANCE_NAME", "env-instance")

	if err := Init(Config{AppName: "explicit-app", LogDir: dir, InstanceName: "explicit-instance"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	Info(context.Background(), "hello")
	Shutdown()

	if _, err := os.Stat(filepath.Join(dir, "explicit-app__explicit-instance.log")); err != nil {
		t.Fatalf("expected explicit-app__explicit-instance.log to exist: %v", err)
	}
	entries, _ := os.ReadDir(otherDir)
	if len(entries) != 0 {
		t.Fatalf("env-provided dir should not have been used once Config was explicit, got %v", entries)
	}
}
