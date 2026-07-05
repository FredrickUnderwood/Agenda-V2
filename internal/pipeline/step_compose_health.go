package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/agenda-v2/config"
	"github.com/agenda-v2/internal/domain"
	"github.com/agenda-v2/internal/runner"
)

const (
	defaultComposeHealthCheckTimeout  = 2 * time.Minute
	defaultComposeHealthCheckInterval = 3 * time.Second
	minComposeHealthCheckInterval     = 500 * time.Millisecond
)

var dockerContainerIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{12,64}$`)

const dockerInspectHealthFormat = "{{.Id}}\t{{.Name}}\t{{index .Config.Labels \"com.docker.compose.service\"}}\t{{.State.Status}}\t{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}\t{{.State.ExitCode}}"

// ComposeHealthCheckStep waits for the containers created by `docker compose
// up -d` to settle. When a service declares Docker's native `healthcheck:`,
// it must become healthy before the pipeline can finish successfully.
type ComposeHealthCheckStep struct {
	Machine        *config.MachineConfig
	WorkDir        string
	ComposeFile    string
	ProjectName    string
	Port           int
	Services       []string
	Timeout        time.Duration
	Interval       time.Duration
	RequireHealthy bool
}

func (s *ComposeHealthCheckStep) Execute(ctx context.Context, rc *RunContext) error {
	timeout := composeHealthTimeout(s.Timeout)
	interval := composeHealthInterval(s.Interval)
	cwd := filepath.Join(rc.LocalPath, s.WorkDir)
	r := runner.New(s.Machine)
	deadline := time.Now().Add(timeout)

	_, _ = fmt.Fprintf(rc.Output, "waiting up to %s for compose project %q health\n", timeout.Round(time.Second), s.ProjectName)
	for attempt := 1; ; attempt++ {
		statuses, err := s.containerStatuses(ctx, r, cwd)
		if err != nil {
			return err
		}

		eval := evaluateComposeHealth(statuses, s.RequireHealthy)
		_, _ = fmt.Fprintf(rc.Output, "[%d] %s\n", attempt, eval.Message)
		if eval.Done {
			return eval.Err
		}

		if !time.Now().Before(deadline) {
			return errors.New(fmt.Sprintf("compose healthcheck timeout after %s: %s", timeout.Round(time.Second), eval.Message))
		}
		wait := interval
		if remaining := time.Until(deadline); remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *ComposeHealthCheckStep) containerStatuses(ctx context.Context, r runner.Runner, cwd string) ([]composeContainerStatus, error) {
	ids, err := s.containerIDs(ctx, r, cwd)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	args := []string{"inspect", "--format", dockerInspectHealthFormat}
	args = append(args, ids...)
	if err := r.RunCmd(ctx, "", "docker", args, &buf); err != nil {
		return nil, errors.New("docker inspect health: " + err.Error() + ": " + strings.TrimSpace(buf.String()))
	}
	return parseComposeContainerStatuses(buf.String())
}

func (s *ComposeHealthCheckStep) containerIDs(ctx context.Context, r runner.Runner, cwd string) ([]string, error) {
	var buf bytes.Buffer
	args := []string{"compose", "-p", s.ProjectName, "-f", s.ComposeFile, "ps", "--all", "-q"}
	args = append(args, s.Services...)
	if err := r.RunCmdEnv(ctx, cwd, composeEnv(s.Port), "docker", args, &buf); err != nil {
		return nil, errors.New("docker compose ps: " + err.Error() + ": " + strings.TrimSpace(buf.String()))
	}
	ids := parseDockerContainerIDs(buf.String())
	if len(ids) == 0 {
		return nil, errors.New("docker compose ps returned no containers for project \"" + s.ProjectName + "\"")
	}
	return ids, nil
}

func composeHealthCheckEnabled(cfg *domain.DockerDeployConfig) bool {
	if cfg == nil || cfg.HealthCheck == nil || cfg.HealthCheck.Enabled == nil {
		return true
	}
	return *cfg.HealthCheck.Enabled
}

func composeHealthTimeoutFromConfig(cfg *domain.DockerHealthCheckConfig) time.Duration {
	if cfg == nil || cfg.TimeoutSeconds <= 0 {
		return defaultComposeHealthCheckTimeout
	}
	return time.Duration(cfg.TimeoutSeconds) * time.Second
}

func composeHealthIntervalFromConfig(cfg *domain.DockerHealthCheckConfig) time.Duration {
	if cfg == nil || cfg.IntervalSeconds <= 0 {
		return defaultComposeHealthCheckInterval
	}
	return time.Duration(cfg.IntervalSeconds) * time.Second
}

func composeHealthRequireHealthy(cfg *domain.DockerHealthCheckConfig) bool {
	return cfg != nil && cfg.RequireHealthy
}

func composeHealthTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultComposeHealthCheckTimeout
	}
	return d
}

func composeHealthInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultComposeHealthCheckInterval
	}
	if d < minComposeHealthCheckInterval {
		return minComposeHealthCheckInterval
	}
	return d
}

func parseDockerContainerIDs(raw string) []string {
	fields := strings.Fields(raw)
	out := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if !dockerContainerIDPattern.MatchString(field) {
			continue
		}
		id := strings.ToLower(field)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

type composeContainerStatus struct {
	ID       string
	Name     string
	Service  string
	State    string
	Health   string
	ExitCode int
}

func parseComposeContainerStatuses(raw string) ([]composeContainerStatus, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("docker inspect health returned no output")
	}
	lines := strings.Split(raw, "\n")
	statuses := make([]composeContainerStatus, 0, len(lines))
	for _, line := range lines {
		parts := strings.Split(line, "\t")
		if len(parts) != 6 {
			return nil, errors.New("unexpected docker inspect health output: " + line)
		}
		exitCode, err := strconv.Atoi(strings.TrimSpace(parts[5]))
		if err != nil {
			return nil, errors.New("unexpected docker inspect exit code \"" + parts[5] + "\": " + err.Error())
		}
		statuses = append(statuses, composeContainerStatus{
			ID:       strings.TrimSpace(parts[0]),
			Name:     strings.TrimPrefix(strings.TrimSpace(parts[1]), "/"),
			Service:  strings.TrimSpace(parts[2]),
			State:    strings.TrimSpace(parts[3]),
			Health:   normalizeHealth(strings.TrimSpace(parts[4])),
			ExitCode: exitCode,
		})
	}
	return statuses, nil
}

type composeHealthEvaluation struct {
	Done    bool
	Message string
	Err     error
}

func evaluateComposeHealth(statuses []composeContainerStatus, requireHealthy bool) composeHealthEvaluation {
	if len(statuses) == 0 {
		err := errors.New("compose healthcheck failed: no containers found")
		return composeHealthEvaluation{Done: true, Message: err.Error(), Err: err}
	}

	summary := formatComposeHealthStatuses(statuses)
	pending := false
	for _, status := range statuses {
		label := statusLabel(status)
		state := strings.ToLower(status.State)
		health := normalizeHealth(status.Health)

		switch state {
		case "running":
		case "created":
			pending = true
			continue
		default:
			err := errors.New(fmt.Sprintf("compose healthcheck failed: %s is %s (exit_code=%d, health=%s)", label, state, status.ExitCode, health))
			return composeHealthEvaluation{Done: true, Message: summary, Err: err}
		}

		switch health {
		case "healthy":
		case "starting":
			pending = true
		case "unhealthy":
			err := errors.New("compose healthcheck failed: " + label + " is unhealthy")
			return composeHealthEvaluation{Done: true, Message: summary, Err: err}
		case "none":
			if requireHealthy {
				err := errors.New("compose healthcheck failed: " + label + " has no Docker healthcheck")
				return composeHealthEvaluation{Done: true, Message: summary, Err: err}
			}
		default:
			pending = true
		}
	}

	if pending {
		return composeHealthEvaluation{Message: "waiting: " + summary}
	}
	return composeHealthEvaluation{Done: true, Message: "healthy: " + summary}
}

func formatComposeHealthStatuses(statuses []composeContainerStatus) string {
	parts := make([]string, 0, len(statuses))
	for _, status := range statuses {
		parts = append(parts, fmt.Sprintf("%s=%s/%s", statusLabel(status), strings.ToLower(status.State), normalizeHealth(status.Health)))
	}
	return strings.Join(parts, ", ")
}

func statusLabel(status composeContainerStatus) string {
	if status.Service != "" {
		return status.Service
	}
	if status.Name != "" {
		return status.Name
	}
	if len(status.ID) >= 12 {
		return status.ID[:12]
	}
	return status.ID
}

func normalizeHealth(health string) string {
	health = strings.ToLower(strings.TrimSpace(health))
	if health == "" || health == "<no value>" {
		return "none"
	}
	return health
}
