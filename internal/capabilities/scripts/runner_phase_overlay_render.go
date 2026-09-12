package scriptgeneration

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// runner_phase_overlay_render.go owns the BLOCKING overlay render boundary.
//
// Why it is its own phase instead of a block inside the audio compile phase:
//
// The render submits the Chronon plan and then WAITS for the remote GPU to
// finish — the single longest wait in a script run. It lived inside
// runAudioCompilePhase, so the audio stage's wall time included a render it
// does not own, the render never appeared on the critical path, and the
// breakdown reported `audio_compile` as the bottleneck with a dominant
// operation that belonged to a different subsystem. The kernel attributes a
// nested stage to its enclosing stage, so the only way to attribute the render
// correctly is to make it a SIBLING phase of the audio stage — which is what
// this file does.
//
// The render itself is unchanged: same gates, same enqueuer, same fail-closed
// outcome. Only the measurement boundary moved.

// runOverlayRenderPhase renders the frozen semantic OverlayPlan and records the
// result on the run.
//
// It is a no-op (and returns true) when the request did not ask for a render, no
// render enqueuer is wired, or the overlay plan was never projected — exactly
// the conditions the in-phase block used to check. When the audio phase was
// already satisfied by a prior attempt, the render was part of that work and is
// not repeated; an already-rendered result is likewise never re-rendered.
func (r *Runner) runOverlayRenderPhase(ctx context.Context, runID string, req GenerateRequest, resumeIdx int, result *GenerateResult) bool {
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
		// The render used to be sequenced inside the audio phase, so a resumed
		// run past that stage has already rendered (or already decided not to).
		// Preserve that contract rather than re-waiting on a completed job.
		return true
	}

	ref, renderErr := r.overlayRenderEnqueuer.EnqueueChrononPlan(ctx, *result.OverlayPlan)
	if renderErr != nil {
		cause := fmt.Errorf("overlay render failed: %w", renderErr)
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
