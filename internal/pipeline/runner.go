package pipeline

import (
	"bytes"
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/agenda-v2/config"
	"github.com/agenda-v2/internal/domain"
	"github.com/agenda-v2/internal/logger"
	"github.com/agenda-v2/internal/service"
)

const defaultMaxOutput = 65536

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
		maxBytes = defaultMaxOutput
	}
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

	var buf bytes.Buffer
	rc := &RunContext{
		Log: log, App: target.App, Branch: target.Branch, CommitSHA: target.CommitSHA,
		Cfg: r.cfg, Output: &buf, LocalPath: localPath,
	}
	execErr := bp.Exec.Execute(ctx, rc)

	finish := time.Now().UTC()
	row.FinishedAt = &finish
	row.DurationMs = finish.Sub(start).Milliseconds()
	row.Output = capOutput(&buf, r.maxOutputBytes)

	// Use a detached context for persistence so a cancelled execution context
	// (e.g. timeout) does not prevent the step status from being written to DB.
	saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer saveCancel()

	if execErr != nil {
		row.Status = domain.StepStatusFailed
		row.ErrorMsg = execErr.Error()
		if row.Output == "" {
			row.Output = execErr.Error()
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
		log.Output = cause.Error()
		log.ErrorMsg = cause.Error()
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = r.logSvc.Finish(finishCtx, log)
	logger.L().Info("pipeline: run failed", zap.Int64("id", log.ID), zap.Error(cause))
}

// capOutput truncates the buffer to the last maxBytes bytes.
func capOutput(buf *bytes.Buffer, maxBytes int) string {
	b := buf.Bytes()
	if len(b) > maxBytes {
		b = b[len(b)-maxBytes:]
	}
	return string(b)
}
