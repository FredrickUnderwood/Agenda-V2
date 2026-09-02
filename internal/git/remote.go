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
	"github.com/FredrickUnderwood/agenda-v2/internal/util"
)

// ResolveSHA expands rev (a commit pin, possibly abbreviated, or "HEAD") to its
// full 40-character object name using the repository at localPath on the target
// machine. machine == nil means execute locally.
//
// A deploy records what this returns as the commit it shipped. Callers pass the
// pin they asked for rather than "HEAD" whenever there is one, so the recorded
// SHA is the commit that was requested rather than whatever the tree happens to
// point at. "HEAD" is only right when nothing was pinned and the branch tip is
// genuinely the answer.
//
// --verify ... ^{commit} makes git fail loudly on an unknown or ambiguous
// abbreviation instead of echoing the input back and exiting non-zero.
func ResolveSHA(ctx context.Context, localPath, rev string, cfg *config.Config, machine *config.MachineConfig) (string, error) {
	if rev == "" {
		rev = "HEAD"
	}
	gitBin := cfg.Git.GitBin
	if gitBin == "" {
		gitBin = "git"
	}

	r := runner.New(machine)
	var buf bytes.Buffer
	if err := r.RunCmd(ctx, "", gitBin, []string{"-C", localPath, "rev-parse", "--verify", rev + "^{commit}"}, &buf); err != nil {
		out := redactTokens(strings.TrimSpace(buf.String()), cfg)
		logger.L().Error("git rev-parse failed",
			zap.String("local_path", localPath),
			zap.String("rev", rev),
			zap.String("output", out),
			zap.Error(err),
		)
		if out != "" {
			return "", errors.New("git rev-parse " + rev + ": " + out)
		}
		return "", err
	}

	sha := strings.TrimSpace(buf.String())
	if sha == "" {
		return "", errors.New("git rev-parse returned nothing for " + rev + " in " + localPath)
	}
	return sha, nil
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
	reused := checkErr == nil

	if err := syncWorkspace(ctx, r, gitBin, authedURL, repoURL, localPath, branch, commitSHA, reused, cfg); err != nil {
		// Wipe localPath and retry once with a fresh clone rather than
		// failing every deploy from here on. Covers two shapes of stale
		// state at that path: a directory that looks like a valid repo
		// (rev-parse succeeded) but whose fetch/reset still failed — e.g.
		// left behind by a previously *failed* clone/fetch (a 403
		// mid-transfer) — and a non-git directory blocking a fresh clone
		// outright (e.g. only a bind-mounted "logs" subdir exists there,
		// from an app's log volume being set up before its repo was ever
		// cloned into the same path).
		logger.L().Warn("git sync failed; removing local workspace and retrying with a fresh clone",
			zap.String("local_path", localPath),
			zap.Error(err),
		)
		var rmBuf bytes.Buffer
		if rmErr := r.RunCmd(ctx, "", "rm", []string{"-rf", localPath}, &rmBuf); rmErr != nil {
			logger.L().Error("failed to remove stale workspace",
				zap.String("local_path", localPath),
				zap.Error(rmErr),
			)
			return err
		}
		return syncWorkspace(ctx, r, gitBin, authedURL, repoURL, localPath, branch, commitSHA, false, cfg)
	}

	return nil
}

// syncWorkspace performs one clone-or-fetch-then-reset cycle. reuse selects
// fetch (existing workspace) vs. clone (fresh/missing workspace).
func syncWorkspace(ctx context.Context, r runner.Runner, gitBin, authedURL, repoURL, localPath, branch, commitSHA string, reuse bool, cfg *config.Config) error {
	var buf bytes.Buffer

	if !reuse {
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

	// FETCH_HEAD is only written by `git fetch` (the reuse branch above) — a
	// plain `git clone` never creates it, it just checks the branch out
	// directly to HEAD. Resetting to FETCH_HEAD after a fresh clone would
	// therefore always fail with "ambiguous argument 'FETCH_HEAD'", even on a
	// perfectly healthy clone.
	resetTarget := "HEAD"
	if reuse {
		resetTarget = "FETCH_HEAD"
	}
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

// ResolveLocalPath derives the PRE-INSTANCE-ISOLATION clone directory for a
// (repoURL, branch) pair: <root>/<host>/<repo path>/<branch>.
//
// Deploys no longer use this — they use ResolveInstanceCodeDir, which gives each
// instance its own checkout. It survives as the resolver for the one thing that
// must still address the old layout: reading logs an instance wrote inside its
// code checkout before the run/ layout existed (see
// ApplicationLogService.legacyInstanceLogs). Do not reach for it in new code,
// and do not "fix" it to include the instance — that would make the back-compat
// path point somewhere that has never existed.
//
// expandTilde controls whether a leading "~" in root is resolved against the
// controller's HOME; pass true only for local execution.
func ResolveLocalPath(repoURL, branch, root string, expandTilde bool) (string, error) {
	if branch == "" {
		return "", errors.New("branch is empty")
	}
	root, err := resolveWorkspaceRoot(root, expandTilde)
	if err != nil {
		return "", err
	}
	host, repoPath, err := normalizeRepoURL(repoURL)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, host, repoPath, branch), nil
}

// srcSubtree is the fixed subdirectory under the workspace root that holds
// per-instance code checkouts. Like runSubtree it is a fixed name at the top of
// the workspace, which is what keeps it from colliding with the legacy layout's
// <host> directories.
const srcSubtree = "src"

// ResolveInstanceCodeDir derives the on-machine clone directory for one
// instance's checkout of one branch:
// <root>/src/<app>/<env>/<instance>/<branch>.
//
// The instance is in the path because a deploy checks out a specific commit
// with `git reset --hard` and then builds from that tree. The old layout keyed
// the directory on (repo, branch) only, so two instances of the same app and
// branch on one machine shared a working tree while holding only their own
// per-instance deploy locks. That was survivable while every instance of a
// batch deployed the same commit, but a rollback resolves each instance's
// target independently: blue rolling back to one commit while green rolls back
// to another would have them resetting and building the same directory
// concurrently, so an instance could build the other's code. git.Pull's
// recovery path (rm -rf and re-clone) could also delete a sibling's tree
// mid-build.
//
// The branch stays the leaf so switching branches does not force a re-clone,
// and everything is slugged - branch included, since it is operator-supplied
// and would otherwise be able to walk out of the workspace with "..".
//
// Instances deployed before this layout have their checkout at the legacy
// ResolveLocalPath location; their next deploy simply clones fresh here, and
// the stale directory is left alone rather than deleted out from under a
// running container.
func ResolveInstanceCodeDir(root, app, env, instance, branch string, expandTilde bool) (string, error) {
	if branch == "" {
		return "", errors.New("branch is empty")
	}
	root, err := resolveWorkspaceRoot(root, expandTilde)
	if err != nil {
		return "", err
	}
	if app == "" {
		return "", errors.New("app is empty")
	}
	if instance == "" {
		instance = "default"
	}
	return filepath.Join(root, srcSubtree, util.Slug(app), util.Slug(env), util.Slug(instance), util.Slug(branch)), nil
}

// runSubtree is the fixed subdirectory under the workspace root that holds
// per-instance runtime state (logs, build artifacts) — kept separate from the
// per-instance code checkouts (which live under <root>/src, see
// ResolveInstanceCodeDir) so a
// re-clone (rm -rf of a checkout) can never wipe an instance's logs, and so the
// path is keyed on the stable (app, env, instance) identity rather than the
// volatile branch. See InstanceLogDir.
const runSubtree = "run"

// ResolveInstanceRunDir derives the on-machine runtime working directory for a
// single deployable instance, rooted at the configured workspace root:
// <root>/run/<app>/<env>/<instance>. Unlike ResolveLocalPath it deliberately
// does NOT include the branch: both the deploy (which bind-mounts the log dir
// into the container) and the log reader must resolve the same path for a given
// instance regardless of which branch/release is currently running on it.
//
// app/env/instance are slugged for filesystem safety; app is operator-supplied
// so it in particular must be normalized.
func ResolveInstanceRunDir(root, app, env, instance string, expandTilde bool) (string, error) {
	root, err := resolveWorkspaceRoot(root, expandTilde)
	if err != nil {
		return "", err
	}
	if app == "" {
		return "", errors.New("app is empty")
	}
	if instance == "" {
		// Mirror domain.DefaultInstanceName without importing domain into this
		// low-level path helper; callers normally pass an already-normalized name.
		instance = "default"
	}
	return filepath.Join(root, runSubtree, util.Slug(app), util.Slug(env), util.Slug(instance)), nil
}

// InstanceLogDir is the "logs" subdirectory of an instance's run dir — the host
// directory bind-mounted onto contract.AgendaContainerLogDir in each of the
// instance's containers, and the directory the node tails on a log request.
func InstanceLogDir(root, app, env, instance string, expandTilde bool) (string, error) {
	runDir, err := ResolveInstanceRunDir(root, app, env, instance, expandTilde)
	if err != nil {
		return "", err
	}
	return filepath.Join(runDir, "logs"), nil
}

// envFilesSubdir is the fixed subdirectory holding an application
// environment's platform-managed files. The leading dot keeps it out of the
// instance-name namespace it shares a parent with: an instance name is slugged
// to ^[a-z0-9][a-z0-9-]*$, so no instance directory can ever collide with it.
const envFilesSubdir = ".files"

// EnvFilesDir is the on-machine directory holding files uploaded for one
// (app, env): <root>/run/<app>/<env>/.files.
//
// It is scoped to the environment rather than to a single instance because the
// files are credentials and configuration the whole environment shares — blue
// and green must read the same key, and giving each instance its own copy would
// only create a way for them to drift apart. It sits outside the code checkout
// for the same reason InstanceLogDir does: a re-clone (or the rm -rf fallback in
// Pull) must not be able to delete it.
func EnvFilesDir(root, app, env string, expandTilde bool) (string, error) {
	root, err := resolveWorkspaceRoot(root, expandTilde)
	if err != nil {
		return "", err
	}
	if app == "" {
		return "", errors.New("app is empty")
	}
	return filepath.Join(root, runSubtree, util.Slug(app), util.Slug(env), envFilesSubdir), nil
}

// resolveWorkspaceRoot validates the configured workspace root and expands a
// leading "~" against the controller's HOME when expandTilde is set (local
// execution only — remote machines must use absolute paths, since ~ would
// expand on the wrong host over SSH).
func resolveWorkspaceRoot(root string, expandTilde bool) (string, error) {
	if root == "" {
		return "", errors.New("workspace_root is empty; configure it on the machine (or globally in agenda-v2.yaml)")
	}
	if strings.HasPrefix(root, "~") {
		if !expandTilde {
			return "", errors.New("workspace_root must be an absolute path for remote machines (no ~ expansion over SSH)")
		}
		return expandHome(root)
	}
	if !filepath.IsAbs(root) {
		return "", errors.New("workspace_root must be an absolute path (e.g. /root/.agenda-v2/workspaces); relative paths produce ambiguous physical locations")
	}
	return root, nil
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
