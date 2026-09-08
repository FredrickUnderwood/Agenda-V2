package pipeline

import (
	"context"
	"errors"
	"io"
	"time"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/outputtail"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

const (
	maxOutputLines = 50
	// Keep stored output/error fields small even when older configs still ask
	// for 16/64 KiB. The marker and final failure reason share this byte budget.
	maxStoredOutputBytes = 4 * 1024
)

// Runner executes a pipeline and persists per-step state. It is stateless
// across runs; each Run call operates on a single DeployLog.
type Runner struct {
	cfg            *config.Config
	logSvc         *service.DeployLogService
	stepSvc        *service.PipelineStepService
	maxOutputBytes int
}

func NewRunner(cfg *config.Config, logSvc *service.DeployLogService, stepSvc *service.PipelineStepService) *Runner {
	maxBytes := cfg.Deploy.MaxOutputBytes
	if maxBytes <= 0 {
		maxBytes = config.DefaultDeployMaxOutputBytes
	}
	maxBytes = min(maxBytes, maxStoredOutputBytes)
	return &Runner{cfg: cfg, logSvc: logSvc, stepSvc: stepSvc, maxOutputBytes: maxBytes}
}

// Run drives the pipeline for an existing DeployLog whose pipeline_step rows
// already exist. Steps already in success/skipped are skipped (supports
// retry from an idx and resume from pause). At every step boundary,
// pause_requested is re-read from DB; if set, the run is marked paused and
// Run returns.
//
// localPath is the resolved on-machine clone directory for the run (provided
// by Builder.Build); it is injected into every step's RunContext so resumed
// runs that skip git_pull still see a populated value.
func (r *Runner) Run(ctx context.Context, log *domain.DeployLog, target *domain.DeployTarget, blueprints []Blueprint, localPath string) {
	logger.L().Info("pipeline: run start",
		zap.Int64("id", log.ID), zap.Int64("application_id", target.App.ID), zap.Int("steps", len(blueprints)))
	if err := r.logSvc.MarkRunning(ctx, log.ID); err != nil {
		r.fail(ctx, log, nil, err)
		return
	}
	log.Status = domain.DeployStatusRunning
	log.PauseRequested = false

	rows, err := r.stepSvc.ListByLog(ctx, log.ID)
	if err != nil {
		r.fail(ctx, log, nil, err)
		return
	}
	if len(rows) != len(blueprints) {
		r.fail(ctx, log, nil, errors.New("step count mismatch between rows and blueprints"))
		return
	}
	if localPath == "" {
		r.fail(ctx, log, nil, errors.New("localPath is empty; Builder must resolve it before Run"))
		return
	}

	for i, bp := range blueprints {
		row := rows[i]
		if row.Status == domain.StepStatusSuccess || row.Status == domain.StepStatusSkipped {
			continue
		}

		if r.shouldPause(ctx, log.ID) {
			logger.L().Info("pipeline: paused at step boundary",
				zap.Int64("id", log.ID), zap.Int("idx", row.Idx), zap.String("name", row.Name))
			_ = r.logSvc.MarkPaused(ctx, log.ID, log.TriggerSHA)
			log.Status = domain.DeployStatusPaused
			return
		}

		if err := r.runOne(ctx, bp, row, log, target, localPath); err != nil {
			r.fail(ctx, log, row, err)
			return
		}
	}

	now := time.Now().UTC()
	log.Status = domain.DeployStatusSuccess
	log.FinishedAt = &now
	log.DurationMs = now.Sub(log.StartedAt).Milliseconds()
	log.Output = ""
	log.ErrorMsg = ""
	if err := r.logSvc.Finish(ctx, log); err != nil {
		return
	}
	logger.L().Info("pipeline: run success", zap.Int64("id", log.ID), zap.Int64("duration_ms", log.DurationMs))
}

// runOne executes a single step with its own output buffer, persists the
// row, and returns a non-nil error on step failure.
func (r *Runner) runOne(ctx context.Context, bp Blueprint, row *domain.PipelineStep, log *domain.DeployLog, target *domain.DeployTarget, localPath string) error {
	logger.L().Info("pipeline: step start",
		zap.Int64("id", log.ID), zap.Int("idx", row.Idx), zap.String("name", row.Name), zap.Int("attempt", row.Attempt))
	start := time.Now().UTC()
	row.Status = domain.StepStatusRunning
	row.StartedAt = &start
	row.FinishedAt = nil
	row.Output = ""
	row.ErrorMsg = ""
	_ = r.stepSvc.Update(ctx, row)

	buf := outputtail.New(r.maxOutputBytes)
	rc := &RunContext{
		Log: log, App: target.App, Branch: target.Branch, CommitSHA: target.CommitSHA,
		Cfg: r.cfg, Output: buf, LocalPath: localPath,
	}
	execErr := bp.Exec.Execute(ctx, rc)

	finish := time.Now().UTC()
	row.FinishedAt = &finish
	row.DurationMs = finish.Sub(start).Milliseconds()
	row.Output = outputtail.Tail(buf.String(), r.maxOutputBytes, maxOutputLines)

	// Use a detached context for persistence so a cancelled execution context
	// (e.g. timeout) does not prevent the step status from being written to DB.
	saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer saveCancel()

	if execErr != nil {
		row.Status = domain.StepStatusFailed
		row.ErrorMsg = outputtail.Tail(execErr.Error(), r.maxOutputBytes, maxOutputLines)
		if row.Output == "" {
			row.Output = row.ErrorMsg
		} else {
			// The release drawer displays output in preference to error_msg.
			// Keep the reason visible alongside the diagnostic tail, within
			// the same storage budget.
			_, _ = io.WriteString(buf, "\n[error] "+row.ErrorMsg+"\n")
			row.Output = outputtail.Tail(buf.String(), r.maxOutputBytes, maxOutputLines)
		}
		_ = r.stepSvc.Update(saveCtx, row)
		logger.L().Info("pipeline: step failed",
			zap.Int64("id", log.ID), zap.Int("idx", row.Idx), zap.String("name", row.Name), zap.Error(execErr))
		return execErr
	}

	row.Status = domain.StepStatusSuccess
	_ = r.stepSvc.Update(saveCtx, row)
	logger.L().Info("pipeline: step success",
		zap.Int64("id", log.ID), zap.Int("idx", row.Idx), zap.String("name", row.Name), zap.Int64("duration_ms", row.DurationMs))
	return nil
}

// shouldPause reloads the log to pick up a pause flag set by another
// request. Errors are swallowed — missing the flag just delays the pause by
// one step.
func (r *Runner) shouldPause(ctx context.Context, id int64) bool {
	cur, err := r.logSvc.GetByID(ctx, id)
	if err != nil {
		return false
	}
	return cur.PauseRequested
}

// fail writes the failed run state. row may be nil (failure before any step
// ran, e.g. config load error).
func (r *Runner) fail(ctx context.Context, log *domain.DeployLog, row *domain.PipelineStep, cause error) {
	now := time.Now().UTC()
	log.Status = domain.DeployStatusFailed
	log.FinishedAt = &now
	log.DurationMs = now.Sub(log.StartedAt).Milliseconds()
	if row != nil {
		log.Output = row.Output
		log.ErrorMsg = row.ErrorMsg
	} else {
		log.Output = outputtail.Tail(cause.Error(), r.maxOutputBytes, maxOutputLines)
		log.ErrorMsg = log.Output
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = r.logSvc.Finish(finishCtx, log)
	logger.L().Info("pipeline: run failed", zap.Int64("id", log.ID), zap.Error(cause))
}
