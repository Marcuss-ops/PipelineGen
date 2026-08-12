package scriptgeneration

import (
	"context"

	"go.uber.org/zap"
)

func (r *Runner) runNormalizePhase(ctx context.Context, runID string, exec ExecutionContext, resumeIdx int) bool {
	// ── Stage 1: Normalize ──────────────────────────────────────
	normalizeStep, startErr := r.startExecutionStep(ctx, exec, "NORMALIZE", "script")
	if startErr != nil {
		r.failRunWithRetry(ctx, runID, StageNormalizing, startErr)
		return false
	}
	if stageSkipped(resumeIdx, StageNormalizing) {
		r.log.Info("skipping completed stage", zap.String("stage", string(StageNormalizing)))
	} else {
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageNormalizing)))
	}
	if stageSkipped(resumeIdx, StageNormalizing) {
		if err := r.skipExecutionStep(ctx, exec, normalizeStep); err != nil {
			r.failRunWithRetry(ctx, runID, StageNormalizing, err)
			return false
		}
	} else if err := r.completeExecutionStep(ctx, exec, normalizeStep); err != nil {
		r.failExecutionStep(ctx, exec, normalizeStep, err)
		r.failRunWithRetry(ctx, runID, StageNormalizing, err)
		return false
	}

	return true
}
