package git

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/runner"
)

// FetchRemoteSHA returns the current commit SHA of the given branch.
// machine == nil means execute locally.
func FetchRemoteSHA(ctx context.Context, repoURL, branch string, cfg *config.Config, machine *config.MachineConfig) (string, error) {
	authedURL, err := injectToken(repoURL, cfg)
	if err != nil {
		return "", err
	}

	gitBin := cfg.Git.GitBin
	if gitBin == "" {
		gitBin = "git"
	}

	r := runner.New(machine)
	var buf bytes.Buffer
	if err := r.RunCmd(ctx, "", gitBin, []string{"ls-remote", "--heads", authedURL, "refs/heads/" + branch}, &buf); err != nil {
		out := redactTokens(strings.TrimSpace(buf.String()), cfg)
		logger.L().Error("git ls-remote failed",
			zap.String("repo_url", repoURL),
			zap.String("branch", branch),
			zap.String("output", out),
			zap.Error(err),
		)
		if out != "" {
			return "", errors.New("git ls-remote: " + out)
		}
		return "", err
	}

	line := strings.TrimSpace(buf.String())
	if line == "" {
		return "", errors.New("branch " + branch + " not found on remote")
	}
	parts := strings.Fields(line)
	if len(parts) < 1 {
		return "", errors.New("unexpected ls-remote output: " + line)
	}
	return parts[0], nil
}

// Pull clones or updates the repo at localPath on the target machine, then
// checks out commitSHA if non-empty, otherwise the branch's latest fetched
// head. machine == nil means execute locally. localPath is the path on the
// target machine (not necessarily the local filesystem).
//
// commitSHA must be reachable from branch's history: clone/fetch here always
// pull the full history of a single branch (no --depth), so any commit that
// was ever branch's HEAD is available locally — this covers "deploy an
// older/newer commit on this branch" and "redeploy the previous commit"
// (rollback), but not a commit that only exists on some other, unrelated
// branch.
func Pull(ctx context.Context, repoURL, localPath, branch, commitSHA string, cfg *config.Config, machine *config.MachineConfig) error {
	authedURL, err := injectToken(repoURL, cfg)
	if err != nil {
		return err
	}

	gitBin := cfg.Git.GitBin
	if gitBin == "" {
		gitBin = "git"
	}

	r := runner.New(machine)
	var buf bytes.Buffer

	checkErr := r.RunCmd(ctx, "", gitBin, []string{"-C", localPath, "rev-parse", "--git-dir"}, &buf)
	buf.Reset()

	if checkErr != nil {
		if err := r.RunCmd(ctx, "", gitBin,
			[]string{"clone", "--branch", branch, "--single-branch", authedURL, localPath},
			&buf,
		); err != nil {
			logger.L().Error("git clone failed",
				zap.String("repo_url", repoURL),
				zap.String("output", redactTokens(buf.String(), cfg)),
				zap.Error(err),
			)
			return errors.New("git clone: " + redactTokens(cmdOutput(&buf, err), cfg))
		}
	} else {
		if err := r.RunCmd(ctx, "", gitBin,
			[]string{"-C", localPath, "fetch", authedURL, branch},
			&buf,
		); err != nil {
			logger.L().Error("git fetch failed",
				zap.String("local_path", localPath),
				zap.String("output", redactTokens(buf.String(), cfg)),
				zap.Error(err),
			)
			return errors.New("git fetch: " + redactTokens(cmdOutput(&buf, err), cfg))
		}
	}

	resetTarget := "FETCH_HEAD"
	if commitSHA != "" {
		resetTarget = commitSHA
	}
	buf.Reset()
	if err := r.RunCmd(ctx, "", gitBin,
		[]string{"-C", localPath, "reset", "--hard", resetTarget},
		&buf,
	); err != nil {
		logger.L().Error("git reset failed",
			zap.String("local_path", localPath),
			zap.String("target", resetTarget),
			zap.String("output", redactTokens(buf.String(), cfg)),
			zap.Error(err),
		)
		return errors.New("git reset: " + redactTokens(cmdOutput(&buf, err), cfg))
	}

	return nil
}

func cmdOutput(buf *bytes.Buffer, err error) string {
	if s := strings.TrimSpace(buf.String()); s != "" {
		return s
	}
	return err.Error()
}

// redactTokens strips any configured git token from s so credentials never
// leak into logs or error strings. It scrubs both the static yaml tokens and,
// when wired, every secret value known to the Setting table.
func redactTokens(s string, cfg *config.Config) string {
	for _, t := range cfg.Git.Tokens {
		if t != "" {
			s = strings.ReplaceAll(s, t, "***")
		}
	}
	if cfg.Git.SecretValues != nil {
		for _, t := range cfg.Git.SecretValues() {
			if t != "" {
				s = strings.ReplaceAll(s, t, "***")
			}
		}
	}
	return s
}

// ResolveLocalPath derives the on-machine clone directory for a (repoURL, branch)
// pair, rooted at the configured workspace root:
// <root>/<host>/<repo path>/<branch>.
//
// expandTilde controls whether a leading "~" in root is resolved against the
// controller's HOME; pass true only for local execution.
func ResolveLocalPath(repoURL, branch, root string, expandTilde bool) (string, error) {
	if root == "" {
		return "", errors.New("workspace_root is empty; configure it on the machine (or globally in agenda-v2.yaml)")
	}
	if branch == "" {
		return "", errors.New("branch is empty")
	}
	if strings.HasPrefix(root, "~") {
		if !expandTilde {
			return "", errors.New("workspace_root must be an absolute path for remote machines (no ~ expansion over SSH)")
		}
		var err error
		root, err = expandHome(root)
		if err != nil {
			return "", err
		}
	} else if !filepath.IsAbs(root) {
		return "", errors.New("workspace_root must be an absolute path (e.g. /root/.agenda-v2/workspaces); relative paths produce ambiguous physical locations")
	}
	host, repoPath, err := normalizeRepoURL(repoURL)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, host, repoPath, branch), nil
}

func expandHome(p string) (string, error) {
	if p == "~" {
		return os.UserHomeDir()
	}
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

// normalizeRepoURL extracts (host, path) from any of:
//   - https://[user[:pass]@]host[:port]/path[.git]
//   - ssh://[user@]host[:port]/path[.git]
//   - git@host:path[.git]            (scp-like syntax)
func normalizeRepoURL(raw string) (string, string, error) {
	if raw == "" {
		return "", "", errors.New("repo url is empty")
	}
	if !strings.Contains(raw, "://") && strings.Contains(raw, "@") && strings.Contains(raw, ":") {
		at := strings.Index(raw, "@")
		colon := strings.Index(raw[at:], ":")
		if colon > 0 {
			host := raw[at+1 : at+colon]
			path := raw[at+colon+1:]
			return host, trimGitSuffix(strings.TrimLeft(path, "/")), nil
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", errors.New("parse repo url: " + err.Error())
	}
	host := u.Hostname()
	if host == "" {
		return "", "", errors.New("repo url " + raw + " has no host")
	}
	return host, trimGitSuffix(strings.TrimLeft(u.Path, "/")), nil
}

func trimGitSuffix(p string) string {
	return strings.TrimSuffix(p, ".git")
}

// injectToken rewrites https://host/path → https://token@host/path. SSH-style
// URLs are rejected by ValidateRepoURL before this ever runs.
func injectToken(rawURL string, cfg *config.Config) (string, error) {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return rawURL, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if u.User != nil {
		return rawURL, nil
	}
	host := u.Hostname()
	// Prefer the runtime resolver (Setting table, rotatable without restart),
	// fall back to the static yaml tokens for bootstrap.
	token := ""
	if cfg.Git.TokenResolver != nil {
		token = cfg.Git.TokenResolver(host)
	}
	if token == "" {
		token = cfg.Git.Tokens[host]
	}
	if token == "" {
		logger.L().Warn("no git token configured for host; private repos will fail with 401/403",
			zap.String("host", host),
			zap.String("hint", "set setting git.token."+host+" (or add git.tokens."+host+" to agenda-v2.yaml) if this repo is private"),
		)
		return rawURL, nil
	}
	u.User = url.User(token)
	return u.String(), nil
}
