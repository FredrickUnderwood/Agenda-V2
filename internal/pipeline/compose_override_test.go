package pipeline

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
)

func TestBuildOverrideYAML_PerServiceEnvDisambiguation(t *testing.T) {
	raw, err := buildOverrideYAML("./logs", "", "myapp", "main", "prod", "default", "", []string{"api", "worker"}, nil)
	if err != nil {
		t.Fatalf("buildOverrideYAML: %v", err)
	}

	var out struct {
		Services map[string]struct {
			Volumes     []string `yaml:"volumes"`
			Environment []string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal override: %v", err)
	}

	for _, svcName := range []string{"api", "worker"} {
		svc, ok := out.Services[svcName]
		if !ok {
			t.Fatalf("expected service %q in override", svcName)
		}
		if len(svc.Volumes) != 1 || svc.Volumes[0] != "./logs:"+contract.AgendaContainerLogDir {
			t.Errorf("service %q volumes = %v", svcName, svc.Volumes)
		}
		wantInstance := "AGENDA_INSTANCE_NAME=default"
		wantService := "AGENDA_SERVICE_NAME=" + svcName
		if !contains(svc.Environment, "AGENDA_ENV=prod") {
			t.Errorf("service %q env missing AGENDA_ENV=prod, got %v", svcName, svc.Environment)
		}
		if !contains(svc.Environment, wantInstance) {
			t.Errorf("service %q env missing %q, got %v", svcName, wantInstance, svc.Environment)
		}
		if !contains(svc.Environment, wantService) {
			t.Errorf("service %q env missing %q, got %v", svcName, wantService, svc.Environment)
		}
	}

	// The two services must not end up with the same AGENDA_SERVICE_NAME —
	// that's the whole point of the fix (they'd otherwise collide on one log
	// file since they share the same mount + AGENDA_APP_NAME/INSTANCE_NAME).
	apiEnv := strings.Join(out.Services["api"].Environment, "\n")
	workerEnv := strings.Join(out.Services["worker"].Environment, "\n")
	if apiEnv == workerEnv {
		t.Fatal("api and worker services got identical env — log files would collide")
	}
}

func TestBuildOverrideYAML_StampsAgendaLabels(t *testing.T) {
	raw, err := buildOverrideYAML("./logs", "", "myapp", "main", "prod", "blue", "", []string{"api", "worker"}, nil)
	if err != nil {
		t.Fatalf("buildOverrideYAML: %v", err)
	}
	var out struct {
		Services map[string]struct {
			Labels []string `yaml:"labels"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal override: %v", err)
	}
	// Every augmented service must carry the (app, env, instance) identity as
	// docker-filterable labels, so a decommission can pick out exactly this
	// instance's containers regardless of branch.
	for _, svcName := range []string{"api", "worker"} {
		labels := out.Services[svcName].Labels
		for _, want := range []string{
			contract.LabelApp + "=myapp",
			contract.LabelEnv + "=prod",
			contract.LabelInstance + "=blue",
		} {
			if !contains(labels, want) {
				t.Errorf("service %q labels missing %q, got %v", svcName, want, labels)
			}
		}
	}
}

func TestBuildOverrideYAML_MetricsAddr(t *testing.T) {
	raw, err := buildOverrideYAML("./logs", "", "myapp", "main", "prod", "default", ":9464", []string{"api"}, nil)
	if err != nil {
		t.Fatalf("buildOverrideYAML: %v", err)
	}
	var out struct {
		Services map[string]struct {
			Environment []string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal override: %v", err)
	}
	if !contains(out.Services["api"].Environment, "AGENDA_METRICS_ADDR=:9464") {
		t.Errorf("expected AGENDA_METRICS_ADDR in env, got %v", out.Services["api"].Environment)
	}
}

func TestBuildOverrideYAML_MetricsAddrEmpty_OmitsVar(t *testing.T) {
	raw, err := buildOverrideYAML("./logs", "", "myapp", "main", "prod", "default", "", []string{"api"}, nil)
	if err != nil {
		t.Fatalf("buildOverrideYAML: %v", err)
	}
	var out struct {
		Services map[string]struct {
			Environment []string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal override: %v", err)
	}
	for _, e := range out.Services["api"].Environment {
		if strings.HasPrefix(e, "AGENDA_METRICS_ADDR=") {
			t.Errorf("expected no AGENDA_METRICS_ADDR when metricsAddr is empty, got %v", out.Services["api"].Environment)
		}
	}
}

// TestAgendaOverrideRelPath_PerInstance locks in the fix for the concurrent
// clobber: instances of the same app/branch share LocalPath, so their override
// files must not collide, or an env-wide parallel batch deploy can bring an
// instance up with a sibling's AGENDA_INSTANCE_NAME and log mount.
func TestAgendaOverrideRelPath_PerInstance(t *testing.T) {
	blue := agendaOverrideRelPath("agenda-example-master-prod-blue")
	def := agendaOverrideRelPath("agenda-example-master-prod-default")
	if blue == def {
		t.Fatalf("override paths collide across instances: %q", blue)
	}
	for _, p := range []string{blue, def} {
		if !strings.HasPrefix(p, ".agenda/") || !strings.HasSuffix(p, ".yml") {
			t.Errorf("unexpected override path shape: %q", p)
		}
	}
}

func contains(vals []string, want string) bool {
	for _, v := range vals {
		if v == want {
			return true
		}
	}
	return false
}

// The environment's managed file directory is mounted read-only and announced
// through AGENDA_FILES_DIR, so an app can find an uploaded credential at a
// fixed in-container path no matter which machine it lands on.
func TestBuildOverrideYAML_MountsEnvFilesDirReadOnly(t *testing.T) {
	raw, err := buildOverrideYAML("/logs", "/run/app/prod/.files", "myapp", "main", "prod", "default", "", []string{"api"}, nil)
	if err != nil {
		t.Fatalf("buildOverrideYAML: %v", err)
	}
	var out struct {
		Services map[string]struct {
			Volumes     []string `yaml:"volumes"`
			Environment []string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal override: %v", err)
	}
	svc := out.Services["api"]

	wantMount := "/run/app/prod/.files:" + contract.AgendaContainerFilesDir + ":ro"
	if !containsString(svc.Volumes, wantMount) {
		t.Errorf("volumes = %v, want %q", svc.Volumes, wantMount)
	}
	wantEnv := contract.AgendaFilesDirEnv + "=" + contract.AgendaContainerFilesDir
	if !containsString(svc.Environment, wantEnv) {
		t.Errorf("environment = %v, want %q", svc.Environment, wantEnv)
	}
}

// An environment with no files configured must not gain an empty bind mount:
// docker would create the source directory itself, as root, and break the first
// upload into it.
func TestBuildOverrideYAML_NoFilesDirMeansNoMount(t *testing.T) {
	raw, err := buildOverrideYAML("/logs", "", "myapp", "main", "prod", "default", "", []string{"api"}, nil)
	if err != nil {
		t.Fatalf("buildOverrideYAML: %v", err)
	}
	if strings.Contains(string(raw), contract.AgendaContainerFilesDir) {
		t.Errorf("override mentions the files dir with no filesDir set:\n%s", raw)
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
