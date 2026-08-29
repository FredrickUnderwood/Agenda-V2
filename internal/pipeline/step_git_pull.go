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
	// Read the SHA back off the working tree rather than trusting the input:
	// it is the full object name even when the operator pinned an
	// abbreviation, and for an unpinned deploy it is what was actually
	// checked out rather than a second, independently resolved view of the
	// branch head. The release records this value (see MarkDeploySucceeded),
	// so it is also exactly what a later rollback will re-pin.
	sha, err := git.ResolveHeadSHA(ctx, rc.LocalPath, rc.Cfg, s.Machine)
	if err != nil {
		return err
	}
	rc.Log.TriggerSHA = sha
	_, _ = rc.Output.WriteString("pulled branch " + rc.Branch + " (commit " + sha + ") into " + rc.LocalPath + "\n")
	return nil
}
