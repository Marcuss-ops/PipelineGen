// Package stockpipeline — orchestrator_resume_test.go (Step 10 C2/4, July 2026).
//
// Verifies the recovery contract: when the steps.Store has pre-Completed
// rows for some dispatchSteps (simulating a prior SIGKILL'd run that
// persisted progress to SQLite before crashing), the orchestrator's
// RunResilient iterates dispatchSteps in pipeline order and SKIPS the
// pre-completed stages via steps.ErrStepAlreadyCompleted on MarkStarted.
// The skip route does NOT re-invoke the step's Run body and does NOT
// call MarkCompleted (terminal-immutability on Completed rows).
//
// godlike/07 fail-closed resume contract:
//   - steps.ErrStepAlreadyCompleted is the canonical typed sentinel
//     surfaced via errors.Is from the orchestrator's MarkStarted
//     branch (NO fmt.Errorf opaque-string wrapping).
//   - The MarkCompleted branch (post-MarkStarted) does NOT fire
//     ErrStepAlreadyCompleted because the orchestrator's
//     `continue`-on-skipped path bypasses it entirely (the step
//     body's Run + the post-Run MarkCompleted are skipped together).
//   - The stepStore row count == len(dispatchSteps) at the end of
//     ResumeAll so operators can SELECT COUNT(*) and confirm
//     "no duplicate stage rows" per the user-spec acceptance.
package stockpipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	_ "github.com/mattn/go-sqlite3" // driver lock per AGENTS.md

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	"github.com/stretchr/testify/require"
)

type stateMutatingStep struct {
	name     string
	mutateFn func(state *RunState)
}

func (s *stateMutatingStep) Name() string { return s.name }
func (s *stateMutatingStep) Run(_ context.Context, runner StepRunner) error {
	s.mutateFn(runner.State())
	return nil
}

// TestOrchestrator_RunResilient_EmptyResultFailsClosed verifies that
// a pre-completed step with no checkpoint payload aborts resume.
func TestOrchestrator_RunResilient_EmptyResultFailsClosed(t *testing.T) {
	db := openOrchestratorResumeTestDB(t)
	store := steps.NewSQLiteStoreWithDB(db)
	ctx := context.Background()
	jobID := "empty-result-test-1"

	planKey := steps.StepKey{
		JobID:            jobID,
		StepKey:          "stock.plan",
		InputFingerprint: legacyStepInputFingerprint(jobID, "stock.plan"),
	}
	require.NoError(t, store.MarkStarted(ctx, planKey))
	require.NoError(t, store.MarkCompleted(ctx, planKey, nil, nil))

	var stageSourcesCalled bool
	stageSourcesStep := &stateAssertingStep{
		name: "stock.stage_sources",
		assertFn: func(state *RunState) error {
			stageSourcesCalled = true
			// State is empty because the legacy checkpoint had no
			// payload; the important thing is that we got here.
			if len(state.Plan) != 0 {
				return fmt.Errorf("expected empty Plan after legacy empty-result resume, got %d entries", len(state.Plan))
			}
			return nil
		},
	}

	dispatchSteps := []Step{
		&stubRecorderStep{name: "stock.plan", count: new(int32)},
		stageSourcesStep,
		&stubRecorderStep{name: "stock.extract_clips", count: new(int32)},
		&stubRecorderStep{name: "stock.compose_chunks", count: new(int32)},
		&stubRecorderStep{name: "stock.publish", count: new(int32)},
	}

	cfg := OrchestratorConfig{JobId: jobID, StepStore: store}
	o := NewTestStockOrchestrator(cfg, resumeStubPlanner{}, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
	o.dispatchSteps = dispatchSteps

	_, err := o.RunResilient(ctx, &RunInput{})
	require.Error(t, err, "an empty completed checkpoint must fail closed")
	require.ErrorIs(t, err, ErrStockResumeStateReadFailed)
	require.False(t, stageSourcesCalled, "downstream steps must not run without canonical checkpoint state")
}

// TestOrchestrator_RunResilient_FutureCheckpointVersionFailsClosed verifies
// that a checkpoint from a newer release is not resumed silently.
func TestOrchestrator_RunResilient_FutureCheckpointVersionFailsClosed(t *testing.T) {
	db := openOrchestratorResumeTestDB(t)
	store := steps.NewSQLiteStoreWithDB(db)
	ctx := context.Background()
	jobID := "future-checkpoint-version-test-1"

	planKey := steps.StepKey{
		JobID:            jobID,
		StepKey:          "stock.plan",
		InputFingerprint: legacyStepInputFingerprint(jobID, "stock.plan"),
	}
	require.NoError(t, store.MarkStarted(ctx, planKey))
	require.NoError(t, store.MarkCompleted(ctx, planKey, []byte(`{"checkpoint_version":2,"Plan":[]}`), nil))

	o := NewTestStockOrchestrator(
		OrchestratorConfig{JobId: jobID, StepStore: store},
		resumeStubPlanner{}, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{},
	)
	o.dispatchSteps = []Step{
		&stubRecorderStep{name: "stock.plan", count: new(int32)},
		&stubRecorderStep{name: "stock.stage_sources", count: new(int32)},
	}

	_, err := o.RunResilient(ctx, &RunInput{})
	require.Error(t, err, "future checkpoint versions must not be resumed silently")
	require.ErrorIs(t, err, ErrStockResumeStateInvalid)
	require.Contains(t, err.Error(), "unsupported checkpoint_version=2")
}

func TestRunStateCheckpoint_CompatibilityShapes(t *testing.T) {
	o := &Orchestrator{}

	versioned, err := o.rehydrateRunState(json.RawMessage(`{"checkpoint_version":1,"Plan":[{"SourceID":"https://example.com/v.mp4"}]}`))
	require.NoError(t, err)
	require.Len(t, versioned.Plan, 1)
	require.Equal(t, "https://example.com/v.mp4", versioned.Plan[0].SourceID)

	_, err = o.rehydrateRunState(json.RawMessage(`{}`))
	require.Error(t, err, "an empty object has no canonical RunState fields")

	_, err = o.rehydrateRunState(json.RawMessage(`null`))
	require.Error(t, err, "JSON null is not a checkpoint object")
}

// TestOrchestrator_RunResilient_MalformedResultFailsClosed verifies
// that a pre-completed step with a non-empty but malformed result
// aborts the run rather than silently resuming with empty state.
func TestOrchestrator_RunResilient_MalformedResultFailsClosed(t *testing.T) {
	db := openOrchestratorResumeTestDB(t)
	store := steps.NewSQLiteStoreWithDB(db)
	ctx := context.Background()
	jobID := "malformed-result-test-1"

	planKey := steps.StepKey{
		JobID:            jobID,
		StepKey:          "stock.plan",
		InputFingerprint: legacyStepInputFingerprint(jobID, "stock.plan"),
	}
	require.NoError(t, store.MarkStarted(ctx, planKey))
	require.NoError(t, store.MarkCompleted(ctx, planKey, []byte("not-json"), nil))

	dispatchSteps := []Step{
		&stubRecorderStep{name: "stock.plan", count: new(int32)},
		&stubRecorderStep{name: "stock.stage_sources", count: new(int32)},
		&stubRecorderStep{name: "stock.extract_clips", count: new(int32)},
		&stubRecorderStep{name: "stock.compose_chunks", count: new(int32)},
		&stubRecorderStep{name: "stock.publish", count: new(int32)},
	}

	cfg := OrchestratorConfig{JobId: jobID, StepStore: store}
	o := NewTestStockOrchestrator(cfg, resumeStubPlanner{}, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
	o.dispatchSteps = dispatchSteps

	_, err := o.RunResilient(ctx, &RunInput{})
	require.Error(t, err, "RunResilient must fail when a completed step has malformed checkpoint data")
	require.ErrorIs(t, err, ErrStockResumeStateInvalid, "error must wrap ErrStockResumeStateInvalid")
}

// TestOrchestrator_RunResilient_IncompatibleCheckpointShapesFailClosed
// covers valid JSON that is not a compatible checkpoint contract.
func TestOrchestrator_RunResilient_IncompatibleCheckpointShapesFailClosed(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{name: "null", payload: "null"},
		{name: "array", payload: "[]"},
		{name: "versioned_empty", payload: `{"checkpoint_version":1}`},
		{name: "versioned_unknown_only", payload: `{"checkpoint_version":1,"future_field":true}`},
		{name: "versioned_wrong_plan_type", payload: `{"checkpoint_version":1,"Plan":"not-a-plan"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openOrchestratorResumeTestDB(t)
			store := steps.NewSQLiteStoreWithDB(db)
			ctx := context.Background()
			jobID := "incompatible-checkpoint-" + tc.name
			key := steps.StepKey{JobID: jobID, StepKey: "stock.plan", InputFingerprint: legacyStepInputFingerprint(jobID, "stock.plan")}
			require.NoError(t, store.MarkStarted(ctx, key))
			require.NoError(t, store.MarkCompleted(ctx, key, []byte(tc.payload), nil))

			o := NewTestStockOrchestrator(OrchestratorConfig{JobId: jobID, StepStore: store}, resumeStubPlanner{}, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
			o.dispatchSteps = []Step{
				&stubRecorderStep{name: "stock.plan", count: new(int32)},
				&stubRecorderStep{name: "stock.stage_sources", count: new(int32)},
			}
			_, err := o.RunResilient(ctx, &RunInput{})
			require.Error(t, err)
			require.ErrorIs(t, err, ErrStockResumeStateInvalid)
		})
	}
}
