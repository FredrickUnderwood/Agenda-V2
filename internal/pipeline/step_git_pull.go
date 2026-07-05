package pipeline

import (
	"context"

	"github.com/agenda-v2/config"
	"github.com/agenda-v2/internal/git"
)

// GitPullStep clones or fast-forwards the repo on the target machine, then
// checks out rc.CommitSHA if pinned, otherwise the branch's latest fetched
// head. Machine == nil means the pull runs locally. LocalPath is provided by
// the Runner.
type GitPullStep struct {
	Machine *config.MachineConfig
}

func (s *GitPullStep) Execute(ctx context.Context, rc *RunContext) error {
	app := rc.App
	if err := git.Pull(ctx, app.RepoURL, rc.LocalPath, rc.Branch, rc.CommitSHA, rc.Cfg, s.Machine); err != nil {
		return err
	}
	sha := rc.CommitSHA
	if sha == "" {
		resolved, err := git.FetchRemoteSHA(ctx, app.RepoURL, rc.Branch, rc.Cfg, s.Machine)
		if err != nil {
			return err
		}
		sha = resolved
	}
	rc.Log.TriggerSHA = sha
	_, _ = rc.Output.WriteString("pulled branch " + rc.Branch + " (commit " + sha + ") into " + rc.LocalPath + "\n")
	return nil
}
