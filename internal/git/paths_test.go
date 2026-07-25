package git

import (
	"path/filepath"
	"testing"
)

func TestResolveInstanceRunDir(t *testing.T) {
	root := "/srv/agenda/workspaces"

	got, err := ResolveInstanceRunDir(root, "agenda-example", "prod", "blue", false)
	if err != nil {
		t.Fatalf("ResolveInstanceRunDir: %v", err)
	}
	want := filepath.Join(root, "run", "agenda-example", "prod", "blue")
	if got != want {
		t.Errorf("run dir = %q, want %q", got, want)
	}
}

func TestInstanceLogDir(t *testing.T) {
	root := "/srv/agenda/workspaces"

	got, err := InstanceLogDir(root, "agenda-example", "prod", "blue", false)
	if err != nil {
		t.Fatalf("InstanceLogDir: %v", err)
	}
	want := filepath.Join(root, "run", "agenda-example", "prod", "blue", "logs")
	if got != want {
		t.Errorf("log dir = %q, want %q", got, want)
	}
}

// The whole point of the run/ layout: an instance's log dir does not depend on
// which branch is currently deployed to it, so the deploy-time bind mount and
// the read-time tail always resolve to the same directory. This is what fixed
// the "blue instance shows 404 while its candidate branch is running" bug.
func TestInstanceLogDir_BranchIndependent(t *testing.T) {
	root := "/srv/agenda/workspaces"

	// Same (app, env, instance); the branch a caller might have in hand is not
	// even an input — assert two conceptually different rollouts land identically.
	a, err := InstanceLogDir(root, "agenda-example", "prod", "blue", false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := InstanceLogDir(root, "agenda-example", "prod", "blue", false)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("log dir not stable across calls: %q vs %q", a, b)
	}
	// And it must contain neither a branch segment nor the code-checkout host/repo
	// path — otherwise a branch change would move it.
	if got := filepath.Base(filepath.Dir(a)); got != "blue" {
		t.Errorf("expected instance as parent of logs, got %q in %q", got, a)
	}
}

func TestInstanceLogDir_SlugsAppName(t *testing.T) {
	root := "/srv/agenda/workspaces"

	// Operator-supplied app names may contain characters unsafe for a path.
	got, err := InstanceLogDir(root, "My App/v2", "prod", "default", false)
	if err != nil {
		t.Fatalf("InstanceLogDir: %v", err)
	}
	want := filepath.Join(root, "run", "my-app-v2", "prod", "default", "logs")
	if got != want {
		t.Errorf("log dir = %q, want %q", got, want)
	}
}

func TestInstanceLogDir_DefaultsEmptyInstance(t *testing.T) {
	root := "/srv/agenda/workspaces"

	got, err := InstanceLogDir(root, "app", "prod", "", false)
	if err != nil {
		t.Fatalf("InstanceLogDir: %v", err)
	}
	want := filepath.Join(root, "run", "app", "prod", "default", "logs")
	if got != want {
		t.Errorf("log dir = %q, want %q", got, want)
	}
}

func TestResolveInstanceRunDir_RemoteRejectsTilde(t *testing.T) {
	// expandTilde=false is the remote-machine path: a ~ root can't be expanded
	// on the wrong host, so it must be rejected rather than silently mis-resolved.
	if _, err := ResolveInstanceRunDir("~/.agenda-v2/workspaces", "app", "prod", "blue", false); err == nil {
		t.Fatal("expected error for ~ root with expandTilde=false")
	}
}

func TestResolveInstanceRunDir_RejectsRelativeRoot(t *testing.T) {
	if _, err := ResolveInstanceRunDir("relative/root", "app", "prod", "blue", true); err == nil {
		t.Fatal("expected error for relative root")
	}
}
