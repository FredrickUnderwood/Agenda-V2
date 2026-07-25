package pipeline

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/runner"
)

// composeEnv builds the env slice passed to docker compose. Port > 0 surfaces
// as APP_PORT so user compose files can use ${APP_PORT:-<fallback>} for the
// host-side port mapping, enabling parallel multi-version deployments.
func composeEnv(port int) []string {
	if port <= 0 {
		return nil
	}
	return []string{fmt.Sprintf("APP_PORT=%d", port)}
}

// composeMetricsEnv mirrors composeEnv for the metrics port: metricsPort > 0
// surfaces as APP_METRICS_PORT so a user compose file can publish
// ${APP_METRICS_PORT:-9464} the same way it already publishes ${APP_PORT}.
// Only relevant to ComposeUpStep — ComposePullStep has nothing to publish.
func composeMetricsEnv(metricsPort int) []string {
	if metricsPort <= 0 {
		return nil
	}
	return []string{fmt.Sprintf("APP_METRICS_PORT=%d", metricsPort)}
}

// ComposePullStep runs `docker compose -p <project> -f <file> pull`.
type ComposePullStep struct {
	Machine     *config.MachineConfig
	WorkDir     string
	ComposeFile string
	ProjectName string
	Port        int
}

func (s *ComposePullStep) Execute(ctx context.Context, rc *RunContext) error {
	cwd := filepath.Join(rc.LocalPath, s.WorkDir)
	r := runner.New(s.Machine)
	args := []string{"compose", "-p", s.ProjectName, "-f", s.ComposeFile, "pull"}
	return r.RunCmdEnv(ctx, cwd, composeEnv(s.Port), "docker", args, rc.Output)
}

// ComposeUpStep runs `docker compose -p <project> -f <file> -f <override> up
// -d --build --remove-orphans [services...]`.
//
// Before invoking compose, it writes an agenda-managed override file
// (<LocalPath>/.agenda/compose.override.yml) that mounts this instance's
// runtime log dir (LogDir, an absolute host path outside the code checkout)
// into each target service at /var/log/agenda and injects
// AGENDA_APP_NAME / AGENDA_LOG_DIR / AGENDA_REPO_BRANCH / AGENDA_ENV /
// AGENDA_INSTANCE_NAME / AGENDA_SERVICE_NAME plus the merged
// (application < env < instance) user env vars.
type ComposeUpStep struct {
	Machine     *config.MachineConfig
	WorkDir     string
	ComposeFile string
	ProjectName string
	Port        int
	MetricsPort int
	Services    []string

	AppName      string
	Branch       string
	EnvName      string
	InstanceName string

	// LogDir is the absolute host path of this instance's runtime log directory
	// (git.InstanceLogDir → <root>/run/<app>/<env>/<instance>/logs). It is
	// created on the target and bind-mounted onto /var/log/agenda in every
	// augmented service, so it lives outside the code checkout and is keyed on
	// the stable (app, env, instance) identity rather than the branch.
	LogDir string

	// Env is the fully merged env var map (application baseline < env-level
	// override < instance-level override) baked into the override file.
	Env map[string]string
}

func (s *ComposeUpStep) Execute(ctx context.Context, rc *RunContext) error {
	metricsAddr := ""
	if s.MetricsPort > 0 {
		metricsAddr = contract.AgendaContainerMetricsAddr
	}
	overridePath, err := writeAgendaOverride(
		ctx, s.Machine,
		rc.LocalPath, s.ComposeFile, s.WorkDir, s.ProjectName, s.LogDir,
		s.AppName, s.Branch, s.EnvName, s.InstanceName, metricsAddr,
		s.Services,
		s.Env,
	)
	if err != nil {
		return err
	}

	cwd := filepath.Join(rc.LocalPath, s.WorkDir)
	overrideArg, err := filepath.Rel(cwd, overridePath)
	if err != nil {
		return err
	}
	r := runner.New(s.Machine)
	args := []string{
		"compose",
		"-p", s.ProjectName,
		"-f", s.ComposeFile,
		"-f", overrideArg,
		"up", "-d", "--build", "--remove-orphans",
	}
	args = append(args, s.Services...)
	env := append(composeEnv(s.Port), composeMetricsEnv(s.MetricsPort)...)
	return r.RunCmdEnv(ctx, cwd, env, "docker", args, rc.Output)
}
