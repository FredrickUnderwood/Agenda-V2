package pipeline

import (
	"context"
	"fmt"
	"io"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

// EnvFileChecker is the slice of service.MachineFileService the compose step
// needs to report on an environment's uploaded files.
type EnvFileChecker interface {
	CheckEnvFiles(ctx context.Context, appID int64, env domain.Environment, machineID int64) ([]service.EnvFileCheck, error)
}

// reportEnvFileState writes, into the deploy log, whether every file the
// console has delivered to this environment is present on this machine with the
// checksum recorded for it.
//
// It is a report, not a gate, and it never returns an error. The file contents
// are not stored by the platform, so it cannot repair a gap; blocking a release
// on someone else's missing credential would trade a silent failure for a stuck
// pipeline. What it buys is that the gap is named, in the deploy log, at the
// moment it starts to matter — rather than surfacing later as an unexplained
// failure inside the application.
//
// It runs as part of compose_up rather than as a pipeline step of its own so
// that adding it does not change the step count of a persisted pipeline, which
// would make every deploy paused or failed across the upgrade un-resumable.
func reportEnvFileState(ctx context.Context, checker EnvFileChecker, appID int64, env domain.Environment, machineID int64, out io.Writer) {
	if checker == nil || appID <= 0 || machineID <= 0 {
		return
	}
	checks, err := checker.CheckEnvFiles(ctx, appID, env, machineID)
	if err != nil {
		fmt.Fprintf(out, "could not check environment files: %v\n", err)
		return
	}
	if len(checks) == 0 {
		return
	}

	problems := 0
	for _, c := range checks {
		switch c.Status {
		case domain.FileVerifyOK:
			fmt.Fprintf(out, "file ok       %s (%s)\n", c.FileName, shortSum(c.Actual))
		case domain.FileVerifyMissing:
			problems++
			fmt.Fprintf(out, "file MISSING  %s — expected at %s; upload it from the app's Files tab\n", c.FileName, c.Path)
		case domain.FileVerifyChanged:
			problems++
			fmt.Fprintf(out, "file CHANGED  %s — on disk %s, expected %s\n", c.FileName, shortSum(c.Actual), shortSum(c.Expected))
		default:
			fmt.Fprintf(out, "file unknown  %s — could not be checked: %s\n", c.FileName, c.Detail)
		}
	}
	if problems > 0 {
		fmt.Fprintf(out, "%d of %d environment files are not in the recorded state; the app may start without them\n",
			problems, len(checks))
	}
}

// shortSum abbreviates a checksum for log output, where the full 64 characters
// carry no more meaning than the first 12 and cost a line wrap.
func shortSum(sum string) string {
	if len(sum) <= 12 {
		return sum
	}
	return sum[:12]
}
