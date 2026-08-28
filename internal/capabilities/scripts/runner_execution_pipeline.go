package scriptgeneration

import "context"

// runExecutionPhases owns the ordered business pipeline. Keeping the phase
// sequence separate from Runner wiring makes the resume/stop contract visible
// and keeps ExecuteWithContext small.
func (r *Runner) runExecutionPhases(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext) {
	e := &executionRun{
		r:     r,
		ctx:   ctx,
		runID: runID,
		req:   req,
		exec:  exec,
	}

	if !e.start() {
		return
	}
	if !e.normalize() {
		return
	}
	if !e.beginMediaPreflight() {
		return
	}
	// The VidRush coordinator wiring lives for the whole run: release it only
	// after every phase that consumes the fan-out completes (or fails).
	if e.coordinator != nil {
		defer r.endVidRush(runID)
	}
	if !e.generate() {
		return
	}
	if !e.joinPreflight() {
		return
	}
	e.ensureResult()
	if !e.translate() {
		return
	}
	if !e.sceneTextReady() {
		return
	}
	if !e.audioCompile() {
		return
	}
	if !e.persist() {
		return
	}
	if !e.documents() {
		return
	}
	e.complete()
}
