package videocreate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	steps "github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// ── Coordinator: the durable stage driver (the §7 state machine) ──────
//
// The coordinator owns ORDER and DURABILITY; each stage only computes.
// For every step of the canonical ladder:
//
//	durably completed (SUCCEEDED/SKIPPED) → skipped entirely
//	otherwise → MarkStarted → run stage → MarkCompleted | MarkFailed
//
// There is no in-memory fast path and no parallel "fire and forget":
// the resumable step store is written BEFORE and AFTER every stage, so
// "server restart at render 40%" can only resume at the first
// non-completed step. A skipped optional step records its skip as a
// durable completion — resume never re-decides it.
type Coordinator struct {
	// Store is the canonical resumable step store. Mandatory.
	Store steps.Store
}

// Run drives the whole ladder to completion. Every returned error is
// typed (ErrWorkflowFailed family) and already recorded in the store.
func (c *Coordinator) Run(ctx context.Context, run *Run) error {
	if c == nil || c.Store == nil {
		return fmt.Errorf("%w: videocreate coordinator has no step store", steps.ErrStoreNotWired)
	}
	if run == nil || run.Job == nil {
		return fmt.Errorf("%w: coordinator run has no job", ErrWorkflowFailed)
	}
	for _, spec := range WorkflowSteps {
		if run.State.Completed(spec.StepKey) {
			continue
		}
		if err := c.runStep(ctx, run, spec); err != nil {
			return err
		}
	}
	run.State.Refresh()
	return nil
}

// runStep executes ONE durable step with the store as the authority.
func (c *Coordinator) runStep(ctx context.Context, run *Run, spec StepSpec) error {
	fn, ok := stageFuncs[spec.StepKey]
	if !ok {
		return fmt.Errorf("%w: no stage implementation for %q", ErrStateCorrupt, spec.StepKey)
	}
	key := steps.StepKey{
		JobID:            run.Job.ID,
		StepKey:          spec.StepKey,
		InputFingerprint: run.stepFingerprint(spec),
	}
	if err := c.Store.MarkStarted(ctx, key); err != nil {
		if errors.Is(err, steps.ErrStepAlreadyCompleted) {
			// Terminal-immutability: a concurrent attempt already
			// finished this step. Treat as done and move on.
			return nil
		}
		return fmt.Errorf("%w: mark %s started: %v", ErrWorkflowFailed, spec.StepKey, err)
	}
	run.stageStarted(spec)
	out, err := fn(ctx, run, spec)
	if err != nil {
		// Record the failure durably FIRST: the error text is what a
		// resumed operator sees.
		_ = c.Store.MarkFailed(ctx, key, err.Error())
		run.stageFailed(spec, err.Error())
		return err
	}
	raw, marshalErr := json.Marshal(out)
	if marshalErr != nil {
		markErr := fmt.Errorf("%w: encode %s output: %v", ErrWorkflowFailed, spec.StepKey, marshalErr)
		_ = c.Store.MarkFailed(ctx, key, markErr.Error())
		run.stageFailed(spec, markErr.Error())
		return markErr
	}
	if err := c.Store.MarkCompleted(ctx, key, raw, nil); err != nil && !errors.Is(err, steps.ErrStepAlreadyCompleted) {
		return fmt.Errorf("%w: mark %s completed: %v", ErrWorkflowFailed, spec.StepKey, err)
	}
	run.applyOutput(spec, out)
	if out.Skipped {
		run.stageSkipped(spec)
	} else {
		run.stageSucceeded(spec)
	}
	return nil
}

// stepFingerprint is stable across retries of the SAME job+payload
// (so MarkStarted idempotency holds and a replay cannot fork the
// history) and distinct across payload versions (the documented
// "new InputFingerprint to restart" path).
func (r *Run) stepFingerprint(spec StepSpec) string {
	return digest.SHA256String(r.RootKey + "|" + spec.StepKey + "|" + r.RequestHash)
}

// applyOutput folds a step's durable output into the live projection so
// later stages in THIS process see it without a re-read.
func (r *Run) applyOutput(spec StepSpec, out StageOutput) {
	rec := r.State.Stages[spec.StepKey]
	if rec == nil {
		return
	}
	rec.Output = out
	rec.Jobs = out.ChildJobs
	rec.Error = ""
	if out.Skipped {
		rec.Status = StageSkipped
	} else {
		rec.Status = StageSucceeded
	}
	r.State.Refresh()
}
