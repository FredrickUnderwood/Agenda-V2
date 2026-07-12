package git

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/config"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, buf.String())
	}
}

// TestPullRecoversFromCorruptedWorkspace reproduces the bug where a
// previously failed clone/fetch (e.g. a 403 mid-transfer) leaves a workspace
// directory that still looks like a valid git repo (rev-parse --git-dir
// succeeds) but fails on fetch/reset — which used to fail every subsequent
// deploy forever. Pull should detect the failure on a reused workspace, wipe
// it, and retry once with a fresh clone.
func TestPullRecoversFromCorruptedWorkspace(t *testing.T) {
	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin")
	local := filepath.Join(tmp, "local")

	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, origin, "init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(origin, "file.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, origin, "add", "file.txt")
	runGit(t, origin, "commit", "-m", "initial")

	cfg := &config.Config{}

	// First Pull: fresh clone into `local`.
	if err := Pull(context.Background(), origin, local, "main", "", cfg, nil); err != nil {
		t.Fatalf("initial Pull failed: %v", err)
	}

	// Corrupt the workspace so it still passes `rev-parse --git-dir` (looks
	// like an existing repo) but `git reset --hard` fails against it — the
	// same shape of damage a mid-transfer clone/fetch failure leaves behind.
	if err := os.WriteFile(filepath.Join(local, ".git", "index"), []byte("not a real index"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second Pull against the corrupted workspace must self-heal: detect the
	// failure, wipe `local`, and succeed via a fresh clone rather than
	// failing forever.
	if err := Pull(context.Background(), origin, local, "main", "", cfg, nil); err != nil {
		t.Fatalf("Pull did not recover from corrupted workspace: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(local, "file.txt"))
	if err != nil {
		t.Fatalf("reading recovered workspace: %v", err)
	}
	if string(got) != "v1" {
		t.Fatalf("recovered workspace content = %q, want %q", got, "v1")
	}
}

// TestPullPersistentFailureReturnsError ensures a genuinely unrecoverable
// failure (bad origin) still surfaces as an error after the one self-heal
// retry, rather than looping or silently succeeding.
func TestPullPersistentFailureReturnsError(t *testing.T) {
	tmp := t.TempDir()
	local := filepath.Join(tmp, "local")
	cfg := &config.Config{}

	err := Pull(context.Background(), filepath.Join(tmp, "does-not-exist"), local, "main", "", cfg, nil)
	if err == nil {
		t.Fatal("expected error for nonexistent origin, got nil")
	}
	if _, statErr := os.Stat(local); !os.IsNotExist(statErr) {
		t.Fatalf("expected no workspace directory to be left behind, stat err = %v", statErr)
	}
}

// TestPullRecoversFromNonGitDirectoryAtPath reproduces a fresh clone blocked
// by a pre-existing, non-empty, non-git directory at localPath — e.g. only a
// log-volume subdirectory was ever created there (bind-mounted before the
// app's repo was cloned into the same path). rev-parse correctly reports "not
// a repo" (reused=false) here, so this exercises the clone-side, not the
// fetch/reset-side, of the self-heal retry.
func TestPullRecoversFromNonGitDirectoryAtPath(t *testing.T) {
	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin")
	local := filepath.Join(tmp, "local")

	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, origin, "init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(origin, "file.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, origin, "add", "file.txt")
	runGit(t, origin, "commit", "-m", "initial")

	// Stray non-git content already sitting at the target path, e.g. from a
	// log-volume mount created before any clone ever happened.
	if err := os.MkdirAll(filepath.Join(local, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "logs", "app.log"), []byte("stray"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	if err := Pull(context.Background(), origin, local, "main", "", cfg, nil); err != nil {
		t.Fatalf("Pull did not recover from stray non-git directory: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(local, "file.txt"))
	if err != nil {
		t.Fatalf("reading recovered workspace: %v", err)
	}
	if string(got) != "v1" {
		t.Fatalf("recovered workspace content = %q, want %q", got, "v1")
	}
}
