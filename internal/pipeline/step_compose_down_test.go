package pipeline

import (
	"strings"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
)

func TestComposeDownScript_LabelPassAlwaysPresent(t *testing.T) {
	// No project name (branch unknown): only the authoritative label-based
	// removal must run — no compose-project or network passes.
	s := &ComposeDownStep{AppName: "myapp", EnvName: "prod", InstanceName: "blue"}
	script := s.script()

	for _, want := range []string{
		"label='" + contract.LabelApp + "=myapp'",
		"label='" + contract.LabelEnv + "=prod'",
		"label='" + contract.LabelInstance + "=blue'",
		"docker rm -f $labeled",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("label pass missing %q\n%s", want, script)
		}
	}
	if strings.Contains(script, "com.docker.compose.project") {
		t.Errorf("no project name given, but script references compose project:\n%s", script)
	}
}

func TestComposeDownScript_ProjectPassesWhenBranchKnown(t *testing.T) {
	s := &ComposeDownStep{
		AppName: "myapp", EnvName: "prod", InstanceName: "blue",
		ProjectName: "myapp-master-prod-blue",
	}
	script := s.script()

	if !strings.Contains(script, "com.docker.compose.project=myapp-master-prod-blue") {
		t.Errorf("expected compose-project pass for known branch:\n%s", script)
	}
	// The compose network cleanup must be tolerant (|| true), never failing the
	// teardown — the label pass is the only authoritative one.
	if !strings.Contains(script, "docker network rm") {
		t.Errorf("expected network cleanup pass:\n%s", script)
	}
}

func TestComposeDownScript_NeverRemovesVolumesByDefault(t *testing.T) {
	// Decommission must preserve data volumes; volume removal only appears when
	// RemoveVolumes is explicitly set (a future "delete data" path).
	s := &ComposeDownStep{
		AppName: "myapp", EnvName: "prod", InstanceName: "blue",
		ProjectName: "myapp-master-prod-blue",
	}
	if strings.Contains(s.script(), "docker volume rm") {
		t.Errorf("decommission script must not remove volumes:\n%s", s.script())
	}

	s.RemoveVolumes = true
	if !strings.Contains(s.script(), "docker volume rm") {
		t.Errorf("RemoveVolumes=true should emit volume removal:\n%s", s.script())
	}
}
