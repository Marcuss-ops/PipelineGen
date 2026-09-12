package scriptgeneration

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// runner_phase_audio_finalize.go owns the tail of the audio boundary: the
// canonical EditingTimelineV1 projection and closing the AUDIO_COMPILE execution
// step.
//
// It runs AFTER the overlay render because the editing timeline's overlay span
// carries the certified artifact (its SHA, Drive link and media contract): the
// timeline is a projection over frozen facts, and the render's certified output
// is one of them. Sequenced before the render it would publish a timeline with
// an empty overlay span.
//
// It is a separate measured stage from audio_compile for the same reason the
// render is: audio_compile must report only the work it owns.

// runAudioFinalizePhase projects the editing timeline from the frozen result and
// closes the execution step the compile phase opened.
func (r *Runner) runAudioFinalizePhase(ctx context.Context, runID string, exec ExecutionContext, state audioCompileState, result *GenerateResult) bool {
	if state.AudioSkipped {
		if err := r.skipExecutionStep(ctx, exec, state.Step); err != nil {
			r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
			return false
		}
		return true
	}
	if result != nil {
		// The canonical EditingTimelineV1 is built from frozen facts. It is the
		// single projection consumed by downstream editing; no component
		// maintains a second independently calculated timeline.
		if et, err := BuildEditingTimeline(result); err != nil {
			cause := fmt.Errorf("editing timeline compilation failed: %w", err)
			r.failExecutionStep(ctx, exec, state.Step, cause)
			r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
			return false
		} else if et != nil {
			result.EditingTimeline = et
		}
		r.log.Info("audio compile complete",
			zap.String("run_id", runID),
			zap.String("audio_mode", string(result.AudioMode)),
		)
	}
	r.checkpoint(ctx, runID, result)
	if err := r.completeExecutionStep(ctx, exec, state.Step); err != nil {
		r.failExecutionStep(ctx, exec, state.Step, err)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
		return false
	}
	return true
}
