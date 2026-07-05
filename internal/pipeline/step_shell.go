package pipeline

import (
	"context"
	"path/filepath"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/runner"
)

// ShellStep runs a sequence of raw shell commands on the target machine.
// WorkDir is interpreted as a path relative to rc.LocalPath (the resolved
// clone directory). Use "." to run at the repo root.
type ShellStep struct {
	Machine  *config.MachineConfig
	WorkDir  string
	Commands []string
}

func (s *ShellStep) Execute(ctx context.Context, rc *RunContext) error {
	cwd := filepath.Join(rc.LocalPath, s.WorkDir)
	r := runner.New(s.Machine)
	for _, cmd := range s.Commands {
		_, _ = rc.Output.WriteString("$ " + cmd + "\n")
		if err := r.RunShell(ctx, cwd, cmd, rc.Output); err != nil {
			return err
		}
	}
	return nil
}
