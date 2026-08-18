package scriptgeneration

import (
	"context"
	"fmt"
)

// persistScript is the capability-owned persistence decision. Storage details
// stay behind ScriptPersistence; this phase only enforces the request flag,
// positive-ID contract, and checkpoint ordering.
func (r *Runner) persistScript(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, resumeIdx int, result *GenerateResult) bool {
	step, startErr := r.startExecutionStep(ctx, exec, "PERSISTENCE", "storage")
	if startErr != nil {
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, startErr)
		return false
	}

	// Idempotent restart: a script already persisted in a prior attempt (even
	// one that crashed before the run reached COMPLETED) must never be written
	// again — the canonical row is reused, so a resume writes 0 duplicate DB
	// rows regardless of which stage it restarts from.
	alreadyPersisted := result != nil && result.ScriptID > 0
	if !req.SaveToDB || stageSkipped(resumeIdx, StagePublishingDocuments) || alreadyPersisted {
		if err := r.skipExecutionStep(ctx, exec, step); err != nil {
			r.failRunWithRetry(ctx, runID, StagePublishingDocuments, err)
			return false
		}
		return true
	}
	if r.scriptPersistence == nil {
		cause := fmt.Errorf("script persistence requested but canonical persistence port is not configured")
		r.failExecutionStep(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, cause)
		return false
	}

	scriptID, err := r.scriptPersistence.Persist(ctx, ScriptPersistenceInput{
		RunID: runID, Request: req, Result: result,
	})
	if err != nil {
		cause := fmt.Errorf("persist script: %w", err)
		r.failExecutionStep(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, cause)
		return false
	}
	if scriptID <= 0 {
		cause := fmt.Errorf("persist script returned invalid script_id=%d", scriptID)
		r.failExecutionStep(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, cause)
		return false
	}
	result.ScriptID = scriptID
	r.checkpoint(ctx, runID, result)
	if err := r.completeExecutionStep(ctx, exec, step); err != nil {
		r.failExecutionStep(ctx, exec, step, err)
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, err)
		return false
	}
	return true
}
