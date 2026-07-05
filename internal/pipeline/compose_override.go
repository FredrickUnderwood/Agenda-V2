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

	"github.com/agenda-v2/config"
	"github.com/agenda-v2/internal/runner"
)

// AgendaContainerLogDir is the in-container log directory that the SDK reads
// from AGENDA_LOG_DIR. agenda mounts <LocalPath>/logs onto this path so the
// host can `cat` the same files the container is writing.
const AgendaContainerLogDir = "/var/log/agenda"

// agendaOverrideRelPath is the override file path under LocalPath. Kept under
// a dot-prefixed dir so it doesn't pollute the user's repo working tree.
const agendaOverrideRelPath = ".agenda/compose.override.yml"

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
// service, mounts mountSource at /var/log/agenda and injects AGENDA_* env
// vars plus any user-defined env vars from userEnv. Keys starting with
// AGENDA_ are silently dropped so the SDK contract can't be broken by a
// misconfigured app.
func buildOverrideYAML(mountSource, appName, branch string, services []string, userEnv map[string]string) ([]byte, error) {
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

	buildEnv := func() []string {
		env := make([]string, 0, len(userKeys)+3)
		for _, k := range userKeys {
			env = append(env, k+"="+userEnv[k])
		}
		env = append(env,
			"AGENDA_APP_NAME="+appName,
			"AGENDA_LOG_DIR="+AgendaContainerLogDir,
			"AGENDA_REPO_BRANCH="+branch,
		)
		return env
	}

	out := map[string]any{
		"services": func() map[string]svc {
			m := make(map[string]svc, len(services))
			for _, name := range services {
				m[name] = svc{
					Volumes:     []string{mountSource + ":" + AgendaContainerLogDir},
					Environment: buildEnv(),
				}
			}
			return m
		}(),
	}
	return yaml.Marshal(out)
}

// composeMountSource computes the bind-mount source for the agenda override,
// relative to <localPath>/<workDir> (compose anchors relative volume paths at
// the compose file's directory).
func composeMountSource(workDir string) string {
	composeDir := filepath.Clean(workDir)
	if composeDir == "" || composeDir == "." {
		return "./logs"
	}
	rel, err := filepath.Rel(composeDir, "logs")
	if err != nil || rel == "" {
		return "./logs"
	}
	if !strings.HasPrefix(rel, "./") && !strings.HasPrefix(rel, "../") {
		rel = "./" + rel
	}
	return rel
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
// it to <localPath>/.agenda/compose.override.yml. Returns the absolute
// override path on the target.
func writeAgendaOverride(
	ctx context.Context,
	machine *config.MachineConfig,
	localPath, composeFile, workDir, appName, branch string,
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

	overrideYAML, err := buildOverrideYAML(composeMountSource(workDir), appName, branch, targets, userEnv)
	if err != nil {
		return "", err
	}

	if err := ensureRemoteDir(ctx, r, filepath.Join(localPath, "logs")); err != nil {
		return "", errors.New("create host log dir: " + err.Error())
	}
	if err := writeRemoteFile(ctx, r, localPath, agendaOverrideRelPath, overrideYAML); err != nil {
		return "", errors.New("write override: " + err.Error())
	}
	return filepath.Join(localPath, agendaOverrideRelPath), nil
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
