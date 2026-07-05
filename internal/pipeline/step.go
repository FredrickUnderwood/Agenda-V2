// Package pipeline runs deployments as an ordered list of Steps, persisting
// per-step status so runs can be paused, resumed, and retried from a specific
// node.
package pipeline

import (
	"bytes"
	"context"

	"github.com/agenda-v2/config"
	"github.com/agenda-v2/internal/domain"
)

// Step is one node in a pipeline. Implementations write their output to
// rc.Output and return an error on failure.
type Step interface {
	Execute(ctx context.Context, rc *RunContext) error
}

// Blueprint describes a step slot in the pipeline. Name/Type are persisted on
// the pipeline_step row; Exec carries the runtime behavior.
type Blueprint struct {
	Name string
	Type string
	Exec Step
}

// RunContext is passed to each step. Log may be mutated by steps (e.g.
// git_pull populates TriggerSHA); changes are persisted when the Runner
// finishes the run.
type RunContext struct {
	Log *domain.DeployLog
	App *domain.Application
	// Branch/CommitSHA come from the triggering ApplicationRelease. CommitSHA
	// empty means "latest Branch HEAD".
	Branch    string
	CommitSHA string
	Cfg       *config.Config
	Output    *bytes.Buffer

	// LocalPath is the on-machine clone directory for this run. Resolved
	// once by Builder.Build from (cfg.WorkspaceRoot, app.RepoURL, branch) and
	// injected into every step's RunContext so resumed/retried runs (where
	// git_pull is skipped) still see a populated value.
	LocalPath string
}
