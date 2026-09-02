package pipeline

import (
	"context"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/git"
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
	// Expand whatever was asked for into a full object name, using the pin when
	// there is one and the checked-out HEAD only when there isn't. The release
	// records this value (see MarkDeploySucceeded) and a later rollback re-pins
	// it verbatim, so it must be the commit this deploy asked for rather than
	// whatever the tree points at by the time this runs.
	sha, err := git.ResolveSHA(ctx, rc.LocalPath, rc.CommitSHA, rc.Cfg, s.Machine)
	if err != nil {
		return err
	}
	rc.Log.TriggerSHA = sha
	_, _ = rc.Output.WriteString("pulled branch " + rc.Branch + " (commit " + sha + ") into " + rc.LocalPath + "\n")
	return nil
}
