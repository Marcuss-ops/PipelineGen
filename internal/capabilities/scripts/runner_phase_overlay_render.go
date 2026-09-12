package scriptgeneration

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// runner_phase_overlay_render.go owns the BLOCKING overlay render boundary.
//
// Why it is its own phase rather than a block inside the audio compile phase:
//
// The render submits the Chronon plan and then WAITS for the remote GPU — the
// single longest wait in a script run. It used to be sequenced inside
// runAudioCompilePhase, so the audio stage's wall time included a render it does
// not own, the render never appeared on the critical path, and the breakdown
// reported `audio_compile` as the bottleneck with a dominant operation borrowed
// from another subsystem. The kernel attributes a nested stage to its enclosing
// stage, so attributing the render correctly requires it to be a SIBLING of the
// audio stage — which is what this file provides.
//
// The render itself is unchanged: same gates, same enqueuer, same fail-closed
// outcome, same execution step on failure. Only the measurement boundary moved.

// runOverlayRenderPhase renders the frozen semantic OverlayPlan and records the
// certified reference on the run.
//
// It returns true without rendering when the request did not ask for a render,
// no render enqueuer is wired, the overlay plan was never projected, the result
// already carries a render, or a prior attempt was already resumed past the
// audio stage (the render used to be part of that stage's work, so a resumed run
// must not re-wait on it).
func (r *Runner) runOverlayRenderPhase(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, resumeIdx int, state audioCompileState, result *GenerateResult) bool {
	if result == nil || result.OverlayPlan == nil {
		return true
	}
	if !req.Render.Enabled || r.overlayRenderEnqueuer == nil {
		return true
	}
	if result.OverlayRender != nil {
		return true
	}
	if stageSkipped(resumeIdx, StageCompilingAudio) {
		return true
	}

	ref, renderErr := r.overlayRenderEnqueuer.EnqueueChrononPlan(ctx, *result.OverlayPlan)
	if renderErr != nil {
		cause := fmt.Errorf("overlay render failed: %w", renderErr)
		// The AUDIO_COMPILE step stays open across the render precisely so a
		// render failure is still reported against the work that produced the
		// plan (its pre-split behaviour).
		r.failExecutionStep(ctx, exec, state.Step, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	result.OverlayRender = &ref
	r.log.Info("overlay render complete",
		zap.String("run_id", runID),
		zap.String("render_job_id", ref.JobID),
		zap.String("status", ref.Status),
	)
	return true
}
