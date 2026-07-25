package pipeline

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/runner"
)

// agendaOverrideRelPath is the override file path under LocalPath, kept under a
// dot-prefixed dir so it doesn't pollute the user's repo working tree.
//
// It MUST be keyed by projectName: instances of the same app+branch on one
// machine share LocalPath (git.ResolveLocalPath keys on repo+branch+machine,
// not instance), so a single fixed filename here lets concurrent per-instance
// deploys — e.g. an env-wide parallel batch (blue + default at once) — clobber
// each other's override file. The loser's `docker compose up` then reads the
// winner's override and comes up with the WRONG AGENDA_INSTANCE_NAME and log-dir
// mount. projectName is unique per app-branch-env-instance, so each instance
// writes (and `-f`-references) its own file.
func agendaOverrideRelPath(projectName string) string {
	return ".agenda/compose.override." + projectName + ".yml"
}

// composeServiceNames returns the top-level keys under `services:` from a
// docker-compose file.
func composeServiceNames(raw []byte) ([]string, error) {
	var top struct {
		Services map[string]yaml.Node `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &top); err != nil {
		return nil, errors.New("parse compose: " + err.Error())
	}
	names := make([]string, 0, len(top.Services))
	for name := range top.Services {
		names = append(names, name)
	}
	return names, nil
}

// buildOverrideYAML returns a docker-compose override that, for every named
// service, mounts logDir (an absolute host path) at /var/log/agenda and injects
// AGENDA_* env vars plus any user-defined env vars from userEnv. Keys starting
// with AGENDA_ are silently dropped so the SDK contract can't be broken by a
// misconfigured app.
//
// logDir is this instance's own runtime log directory
// (git.InstanceLogDir → <root>/run/<app>/<env>/<instance>/logs), so every
// service in this compose file shares it but no other instance does — the mount
// itself isolates instances. envName / instanceName and each service's own name
// are injected as AGENDA_ENV / AGENDA_INSTANCE_NAME / AGENDA_SERVICE_NAME — the
// SDK's log sink attaches them as env/instance/service fields on every line and
// the metrics SDK labels with them, so an app's logs and metrics carry matching
// identity. (instanceName also still appears in the log filename for
// readability, but is no longer load-bearing for log isolation.)
//
// metricsAddr, when non-empty (target has MetricsEnabled), additionally
// injects AGENDA_METRICS_ADDR so sdk/go/metric knows where to listen; empty
// omits the var entirely, leaving metrics registered-but-unserved by default.
func buildOverrideYAML(logDir, appName, branch, envName, instanceName, metricsAddr string, services []string, userEnv map[string]string) ([]byte, error) {
	type svc struct {
		Volumes     []string `yaml:"volumes"`
		Environment []string `yaml:"environment"`
	}

	userKeys := make([]string, 0, len(userEnv))
	for k := range userEnv {
		if k == "" || strings.HasPrefix(k, "AGENDA_") {
			continue
		}
		userKeys = append(userKeys, k)
	}
	sort.Strings(userKeys)

	buildEnv := func(serviceName string) []string {
		env := make([]string, 0, len(userKeys)+6)
		for _, k := range userKeys {
			env = append(env, k+"="+userEnv[k])
		}
		env = append(env,
			"AGENDA_APP_NAME="+appName,
			"AGENDA_LOG_DIR="+contract.AgendaContainerLogDir,
			"AGENDA_REPO_BRANCH="+branch,
			"AGENDA_ENV="+envName,
			"AGENDA_INSTANCE_NAME="+instanceName,
			"AGENDA_SERVICE_NAME="+serviceName,
		)
		if metricsAddr != "" {
			env = append(env, "AGENDA_METRICS_ADDR="+metricsAddr)
		}
		return env
	}

	out := map[string]any{
		"services": func() map[string]svc {
			m := make(map[string]svc, len(services))
			for _, name := range services {
				m[name] = svc{
					Volumes:     []string{logDir + ":" + contract.AgendaContainerLogDir},
					Environment: buildEnv(name),
				}
			}
			return m
		}(),
	}
	return yaml.Marshal(out)
}

// writeRemoteFile writes content to <dir>/<relPath> on whichever machine the
// runner targets. base64 piping avoids shell-escaping pitfalls.
func writeRemoteFile(ctx context.Context, r runner.Runner, dir, relPath string, content []byte) error {
	encoded := base64.StdEncoding.EncodeToString(content)
	full := filepath.Join(dir, relPath)
	parent := filepath.Dir(full)
	shellCmd := "mkdir -p " + shQuote(parent) + " && printf %s " + shQuote(encoded) + " | base64 -d > " + shQuote(full)
	return r.RunShell(ctx, "", shellCmd, &bytes.Buffer{})
}

// readRemoteFile cats a file from the runner's machine into memory.
func readRemoteFile(ctx context.Context, r runner.Runner, path string) ([]byte, error) {
	var buf bytes.Buffer
	if err := r.RunCmd(ctx, "", "cat", []string{path}, &buf); err != nil {
		return nil, errors.New("read " + path + ": " + err.Error())
	}
	return buf.Bytes(), nil
}

// ensureRemoteDir mkdir -p's a directory on the runner's machine.
func ensureRemoteDir(ctx context.Context, r runner.Runner, path string) error {
	return r.RunCmd(ctx, "", "mkdir", []string{"-p", path}, &bytes.Buffer{})
}

// writeAgendaOverride is the high-level helper used by ComposeUpStep: reads
// the user's compose file, picks the services to augment (explicit list when
// non-empty, otherwise all of them), generates the override YAML, and writes
// it to <localPath>/.agenda/compose.override.<projectName>.yml. Returns the
// absolute override path on the target.
//
// projectName keys the override filename so concurrent per-instance deploys
// sharing localPath don't clobber each other (see agendaOverrideRelPath).
//
// logDir is the absolute host path of this instance's runtime log directory
// (git.InstanceLogDir); it is created on the target and bind-mounted into every
// augmented service. It lives outside localPath (the code checkout) so a
// re-clone can't wipe it.
func writeAgendaOverride(
	ctx context.Context,
	machine *config.MachineConfig,
	localPath, composeFile, workDir, projectName, logDir, appName, branch, envName, instanceName, metricsAddr string,
	servicesFilter []string,
	userEnv map[string]string,
) (string, error) {
	r := runner.New(machine)

	composeAbs := filepath.Join(localPath, workDir, composeFile)
	composeRaw, err := readRemoteFile(ctx, r, composeAbs)
	if err != nil {
		return "", err
	}

	all, err := composeServiceNames(composeRaw)
	if err != nil {
		return "", err
	}

	targets := all
	if len(servicesFilter) > 0 {
		set := make(map[string]struct{}, len(all))
		for _, n := range all {
			set[n] = struct{}{}
		}
		targets = targets[:0]
		for _, name := range servicesFilter {
			if _, ok := set[name]; ok {
				targets = append(targets, name)
			}
		}
	}
	if len(targets) == 0 {
		return "", errors.New("no services to augment in " + composeAbs)
	}

	overrideYAML, err := buildOverrideYAML(logDir, appName, branch, envName, instanceName, metricsAddr, targets, userEnv)
	if err != nil {
		return "", err
	}

	if err := ensureRemoteDir(ctx, r, logDir); err != nil {
		return "", errors.New("create host log dir: " + err.Error())
	}
	relPath := agendaOverrideRelPath(projectName)
	if err := writeRemoteFile(ctx, r, localPath, relPath, overrideYAML); err != nil {
		return "", errors.New("write override: " + err.Error())
	}
	return filepath.Join(localPath, relPath), nil
}

// shQuote single-quotes s for safe inclusion in a /bin/sh command.
func shQuote(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'', '\\', '\'', '\'')
			continue
		}
		out = append(out, s[i])
	}
	out = append(out, '\'')
	return string(out)
}
