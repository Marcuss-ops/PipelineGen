package scriptgeneration

import (
	"context"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// ── Internal helpers ────────────────────────────────────────────────

// updateStage persists the stage transition.
func (r *Runner) updateStage(ctx context.Context, runID string, status RunStatus, stage Stage) error {
	return r.repo.UpdateStage(ctx, runID, status, stage)
}

// checkpoint saves the partial result to the repository.
// Errors are logged but not propagated (best-effort checkpoint).
func (r *Runner) checkpoint(ctx context.Context, runID string, result *GenerateResult) {
	if err := r.repo.SavePartialResult(ctx, runID, result); err != nil {
		r.log.Warn("checkpoint save failed",
			zap.String("run_id", runID),
			zap.Error(err),
		)
	}
}

// failRunWithRetry marks the run as FAILED and persists all failure
// metadata (error_code, failed_stage, attempt_count, next_retry_at)
// via the repository's FailRun method.
//
// P0 verdetto contract: every failure persists:
//   - error_code   — stable machine-readable code
//   - failed_stage — which stage failed
//   - attempt_count — incremented retry count
//   - next_retry_at — exponential backoff window (nil when exhausted)
func (r *Runner) failRunWithRetry(ctx context.Context, runID string, failedStage Stage, err error) {
	r.log.Error("scriptgeneration: stage failed",
		zap.String("run_id", runID),
		zap.String("failed_stage", string(failedStage)),
		zap.Error(err),
	)

	// Derive a stable error code from the error chain.
	errorCode := deriveErrorCode(err, failedStage)

	// Read current run to get attempt count.
	run, readErr := r.repo.Get(ctx, runID)
	attempt := 0
	if readErr == nil && run != nil {
		attempt = run.AttemptCount
	}

	// Compute the next retry attempt (1-based for display, 0-based for storage).
	nextAttempt := attempt + 1
	var nextRetryAt *time.Time
	if nextAttempt <= MaxRetries {
		delay := RetryDelay(attempt)
		now := time.Now().UTC()
		t := now.Add(delay)
		nextRetryAt = &t
		r.log.Info("retry scheduled",
			zap.String("run_id", runID),
			zap.Int("attempt", nextAttempt),
			zap.Duration("delay", delay),
			zap.Time("next_retry_at", t),
		)
	} else {
		r.log.Warn("max retries exhausted",
			zap.String("run_id", runID),
			zap.Int("attempts", attempt),
		)
	}

	// Persist all failure metadata atomically via FailRun.
	// AttemptCount is incremented to reflect this failed attempt.
	if failErr := r.repo.FailRun(ctx, FailRunInput{
		RunID:        runID,
		FailedStage:  failedStage,
		ErrorCode:    errorCode,
		ErrorMessage: err.Error(),
		AttemptCount: attempt + 1,
		NextRetryAt:  nextRetryAt,
	}); failErr != nil {
		r.log.Error("failed to persist run failure",
			zap.String("run_id", runID),
			zap.Error(failErr),
		)
	}
}

// completeRun marks the run as COMPLETED and saves the final result.
func (r *Runner) completeRun(ctx context.Context, runID string, result *GenerateResult) {
	r.log.Info("scriptgeneration: run completed",
		zap.String("run_id", runID),
		zap.Int("scene_count", len(result.Scenes)),
	)
	// Print the canonical critical path + bottleneck percentage from the live
	// run clock so operators see the dominant sequential chain per run without
	// querying /api/jobs/:id/full. Best-effort: a unit runtime with no Run
	// bound to ctx is a silent no-op (instrumentation never changes behaviour).
	if run := kernobs.FromContext(ctx); run != nil {
		sum := run.TimingSummary()
		if len(sum.CriticalPath) > 0 {
			r.log.Info("scriptgeneration: critical path",
				zap.String("run_id", runID),
				zap.String("critical_path", sum.FormatCriticalPath()),
				zap.String("bottleneck_stage", sum.BottleneckStage),
				zap.Float64("bottleneck_percent", sum.BottleneckPercent),
				zap.Int64("wall_ms", sum.WallMs),
			)
		}
	}
	r.checkpoint(ctx, runID, result)
	if updateErr := r.repo.UpdateStage(ctx, runID, RunStatusCompleted, StageCompleted); updateErr != nil {
		r.log.Error("failed to persist run completion",
			zap.String("run_id", runID),
			zap.Error(updateErr),
		)
	}
}
