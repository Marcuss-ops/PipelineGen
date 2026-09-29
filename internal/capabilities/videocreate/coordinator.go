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
			run.reportProgress(CurrentStageProgress(run.State), "resumed: "+run.State.Describe())
			break
		}
	}
	for _, spec := range WorkflowSteps {
		if run.State.Completed(spec.StepKey) {
			// Durable-complete from an earlier attempt: re-emit its canonical
			// stage row so a RESUMED job's stage table is complete instead of
			// starting empty (see Run.stageAlreadyCompleted). The transient
			// progress bar is deliberately untouched here.
			run.stageAlreadyCompleted(ctx, spec)
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
			// finished this step. Reload its durable output before moving
			// on so later stages use the winner's facts, not stale state.
			if err := c.restoreConcurrentCompletion(ctx, run, spec); err != nil {
				return err
			}
			return nil
		}
		return fmt.Errorf("%w: mark %s started: %v", ErrWorkflowFailed, spec.StepKey, err)
	}
	run.stageStarted(ctx, spec)
	out, err := fn(ctx, run, spec)
	if err != nil {
		// Record the failure durably FIRST: the error text is what a
		// resumed operator sees. A terminal completion by another attempt
		// wins over this late failure and must restore its projection.
		if markErr := c.Store.MarkFailed(ctx, key, err.Error()); errors.Is(markErr, steps.ErrStepAlreadyCompleted) {
			return c.restoreConcurrentCompletion(ctx, run, spec)
		}
		run.stageFailed(ctx, spec, err.Error())
		return err
	}
	raw, marshalErr := json.Marshal(out)
	if marshalErr != nil {
		markErr := fmt.Errorf("%w: encode %s output: %v", ErrWorkflowFailed, spec.StepKey, marshalErr)
		if storeErr := c.Store.MarkFailed(ctx, key, markErr.Error()); errors.Is(storeErr, steps.ErrStepAlreadyCompleted) {
			return c.restoreConcurrentCompletion(ctx, run, spec)
		}
		run.stageFailed(ctx, spec, markErr.Error())
		return markErr
	}
	if err := c.Store.MarkCompleted(ctx, key, raw, nil); err != nil {
		if !errors.Is(err, steps.ErrStepAlreadyCompleted) {
			return fmt.Errorf("%w: mark %s completed: %v", ErrWorkflowFailed, spec.StepKey, err)
		}
		// A concurrent attempt won the terminal transition. Reload its
		// output rather than projecting this attempt's potentially different
		// result into the live run.
		return c.restoreConcurrentCompletion(ctx, run, spec)
	}
	run.applyOutput(spec, out)
	if out.Skipped {
		run.stageSkipped(ctx, spec)
	} else {
		run.stageSucceeded(ctx, spec)
	}
	return nil
}

// restoreConcurrentCompletion reloads the durable winner after a second
// worker discovers that MarkStarted raced with a terminal completion. The
// local state snapshot predates that completion, so carrying it forward would
// make the next step operate on missing or stale facts.
func (c *Coordinator) restoreConcurrentCompletion(ctx context.Context, run *Run, spec StepSpec) error {
	rows, err := c.Store.ListByJob(ctx, run.Job.ID)
	if err != nil {
		return fmt.Errorf("%w: reload %s after concurrent completion: %v", ErrWorkflowFailed, spec.StepKey, err)
	}
	state, err := StateFromSteps(rows)
	if err != nil {
		return err
	}
	if !state.Completed(spec.StepKey) {
		return fmt.Errorf("%w: store reported %s completed but no terminal row was found", ErrStateCorrupt, spec.StepKey)
	}
	run.State = state
	run.Facts = Facts{}
	if err := RehydrateFacts(ctx, run); err != nil {
		return err
	}
	run.stageAlreadyCompleted(ctx, spec)
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
