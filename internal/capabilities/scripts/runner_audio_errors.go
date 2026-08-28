package scriptgeneration

import "context"

// failAudioCompileStep records both the execution-step failure and the
// run-level retryable failure. All audio validation/rendering branches use the
// same fail-closed boundary.
func (r *Runner) failAudioCompileStep(ctx context.Context, runID string, exec ExecutionContext, step ExecutionStep, cause error) bool {
	r.failExecutionStep(ctx, exec, step, cause)
	r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
	return false
}
