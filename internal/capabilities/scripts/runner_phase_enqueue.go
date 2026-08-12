package scriptgeneration

import (
	"context"
	"fmt"
	"go.uber.org/zap"
)

func (r *Runner) runEnqueuePhase(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, resumeIdx int, result *GenerateResult) bool {
	// ── Stage 7: Enqueue Render ─────────────────────────────────
	renderStep, startErr := r.startExecutionStep(ctx, exec, "VELOX_ENQUEUE", "render")
	if startErr != nil {
		r.failRunWithRetry(ctx, runID, StageEnqueuingRender, startErr)
		return false
	}
	renderSkipped := stageSkipped(resumeIdx, StageEnqueuingRender) || !req.RenderVideo || r.renderEnqueuer == nil
	if !renderSkipped {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageEnqueuingRender); err != nil {
			r.failExecutionStep(ctx, exec, renderStep, err)
			r.failRunWithRetry(ctx, runID, StageEnqueuingRender, err)
			return false
		}
		if result.RenderPlan == nil {
			cause := fmt.Errorf("render enqueue requires a canonical RenderPlan")
			r.failExecutionStep(ctx, exec, renderStep, cause)
			r.failRunWithRetry(ctx, runID, StageEnqueuingRender, cause)
			return false
		}
		if err := result.RenderPlan.Validate(); err != nil {
			cause := fmt.Errorf("render plan validation failed before enqueue: %w", err)
			r.failExecutionStep(ctx, exec, renderStep, cause)
			r.failRunWithRetry(ctx, runID, StageEnqueuingRender, cause)
			return false
		}
		renderRef, err := r.renderEnqueuer.Enqueue(ctx, *result)
		if err != nil {
			cause := fmt.Errorf("enqueue render failed: %w", err)
			r.failExecutionStep(ctx, exec, renderStep, cause)
			r.failRunWithRetry(ctx, runID, StageEnqueuingRender, cause)
			return false
		}
		if renderRef.Status == "" {
			renderRef.Status = "QUEUED"
		}
		result.RenderJob = &renderRef
		if result.RenderPlan.FinalAudio != nil {
			if err := r.attachInputAsset(ctx, exec, renderStep.StepID, result.RenderPlan.FinalAudio.AssetID, 0); err != nil {
				r.failExecutionStep(ctx, exec, renderStep, err)
				r.failRunWithRetry(ctx, runID, StageEnqueuingRender, err)
				return false
			}
		}
		if err := r.recordExecutionMetric(ctx, exec, renderStep.StepID, "render_duration_frames", float64(result.RenderPlan.DurationFrames), "frames"); err != nil {
			r.failExecutionStep(ctx, exec, renderStep, err)
			r.failRunWithRetry(ctx, runID, StageEnqueuingRender, err)
			return false
		}
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageEnqueuingRender)))
	}
	if renderSkipped {
		if err := r.skipExecutionStep(ctx, exec, renderStep); err != nil {
			r.failRunWithRetry(ctx, runID, StageEnqueuingRender, err)
			return false
		}
	} else if err := r.completeExecutionStep(ctx, exec, renderStep); err != nil {
		r.failExecutionStep(ctx, exec, renderStep, err)
		r.failRunWithRetry(ctx, runID, StageEnqueuingRender, err)
		return false
	}

	return true
}
