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
	"sync/atomic"
	"testing"

	_ "github.com/mattn/go-sqlite3" // driver lock per AGENTS.md

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrchestrator_RunResilient_NewStepFailureMarkFailed(t *testing.T) {
	db := openOrchestratorResumeTestDB(t)
	store := steps.NewSQLiteStoreWithDB(db)
	ctx := context.Background()
	jobID := "fail-test"

	// Pre-Complete the first 2 stages (success path via skip).
	for _, name := range []string{"stock.plan", "stock.stage_sources"} {
		k := steps.StepKey{
			JobID:            jobID,
			StepKey:          name,
			InputFingerprint: legacyStepInputFingerprint(jobID, name),
		}
		require.NoError(t, store.MarkStarted(ctx, k))
		require.NoError(t, store.MarkCompleted(ctx, k, []byte(`{"checkpoint_version":1,"Plan":[]}`), []byte(`[]`)))
	}

	// Step 3 (NEW) throws; step 4 + 5 should NOT be called.
	counters := [5]*int32{}
	for i := range counters {
		counters[i] = new(int32)
	}
	dispatchSteps := []Step{
		&stubRecorderStep{name: "stock.plan", count: counters[0]},
		&stubRecorderStep{name: "stock.stage_sources", count: counters[1]},
		&stubRecorderStepThrowing{name: "stock.extract_clips", count: counters[2]},
		&stubRecorderStep{name: "stock.compose_chunks", count: counters[3]},
		&stubRecorderStep{name: "stock.publish", count: counters[4]},
	}

	cfg := OrchestratorConfig{JobId: jobID, StepStore: store}
	o := NewTestStockOrchestrator(cfg, resumeStubPlanner{}, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
	o.dispatchSteps = dispatchSteps

	_, err := o.RunResilient(ctx, &RunInput{})
	require.Error(t, err, "RunResilient should surface the stub-thrown error")
	require.ErrorIs(t, err, assertErrRun,
		"RunResilient should wrap the stub error so callers can errors.Is")

	// Pre-Completed stages SKIPPED via ErrStepAlreadyCompleted.
	assert.Equal(t, int32(0), atomic.LoadInt32(counters[0]),
		"stock.plan was pre-Completed; Run MUST NOT be called")
	assert.Equal(t, int32(0), atomic.LoadInt32(counters[1]),
		"stock.stage_sources was pre-Completed; Run MUST NOT be called")

	// The throwing stage WAS called (it's NEW).
	assert.Equal(t, int32(1), atomic.LoadInt32(counters[2]),
		"stock.extract_clips was NEW; throwing Run MUST be called once")

	// Subsequent stages NOT called (orchestrator aborts on error).
	assert.Equal(t, int32(0), atomic.LoadInt32(counters[3]),
		"stock.compose_chunks MUST NOT be called (orchestrator aborted)")
	assert.Equal(t, int32(0), atomic.LoadInt32(counters[4]),
		"stock.publish MUST NOT be called (orchestrator aborted)")

	// stepStore state: 2 pre-Completed + 1 Failed (from MarkFailed
	// on the throwing stage). 2 remaining stages have no rows.
	rows, listErr := store.ListByJob(ctx, jobID)
	require.NoError(t, listErr)
	assert.Equal(t, 3, len(rows),
		"stepStore has 3 rows: 2 pre-Completed + 1 Failed")

	// Find the Failed row + verify status + last_error.
	var failedRow *steps.StepState
	for i := range rows {
		if rows[i].Status == steps.StatusFailed {
			failedRow = &rows[i]
			break
		}
	}
	require.NotNil(t, failedRow,
		"stepStore MUST have a Failed row for stock.extract_clips")
	assert.Equal(t, "stock.extract_clips", failedRow.StepKey)
	assert.Equal(t, 1, failedRow.Attempt)
	// lease_until is cleared on MarkFailed per godlike/07. The
	// canonical StepState struct does not expose lease_until as a
	// field (it is part of the row state but kept out of the typed
	// surface), so we verify the clearing via raw SQL — the SQLite
	// impl writes '' explicitly on the Failed transition
	// (see TestSQLiteStore_MarkFailed_ClearsLease for the
	// store-level coverage).
	var failedLease string
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT lease_until FROM execution_steps WHERE id = ?`,
		failedRow.ID).Scan(&failedLease),
		"query failedRow lease_until for verify")
	assert.Equal(t, "", failedLease,
		"Failed stage clears lease_until per godlike/07 contract")
}

// TestOrchestrator_RunResilient_RehydratesRunState verifies that
// pre-completed steps restore their produced RunState so later
// steps see the accumulated state. This is the core crash-resume
// contract: a prior run persisted Plan in stock.plan's checkpoint,
// and the resumed run's stock.stage_sources step must observe it.
func TestOrchestrator_RunResilient_RehydratesRunState(t *testing.T) {
	db := openOrchestratorResumeTestDB(t)
	store := steps.NewSQLiteStoreWithDB(db)
	ctx := context.Background()
	jobID := "rehydrate-test-1"

	// Simulate a prior run that completed stock.plan and persisted
	// its produced RunState (including the Plan slice).
	planState := RunState{
		Plan: []ClipPlan{
			{
				SourceID:        "https://example.com/video.mp4",
				OutputLogicalID: "planner:test:0",
				StartSec:        0,
				EndSec:          5,
			},
		},
	}
	planBytes, marshalErr := json.Marshal(planState)
	require.NoError(t, marshalErr, "marshal planState for pre-completed row")

	planKey := steps.StepKey{
		JobID:            jobID,
		StepKey:          "stock.plan",
		InputFingerprint: legacyStepInputFingerprint(jobID, "stock.plan"),
	}
	require.NoError(t, store.MarkStarted(ctx, planKey))
	require.NoError(t, store.MarkCompleted(ctx, planKey, planBytes, nil))

	// stage_sources step asserts that the Plan was rehydrated.
	var stageSourcesCalled bool
	stageSourcesStep := &stateAssertingStep{
		name: "stock.stage_sources",
		assertFn: func(state *RunState) error {
			stageSourcesCalled = true
			if len(state.Plan) != 1 {
				return fmt.Errorf("expected 1 rehydrated plan entry, got %d", len(state.Plan))
			}
			if state.Plan[0].SourceID != "https://example.com/video.mp4" {
				return fmt.Errorf("expected rehydrated SourceID %q, got %q",
					"https://example.com/video.mp4", state.Plan[0].SourceID)
			}
			return nil
		},
	}

	// extract_clips step asserts that state is still present after
	// the previous rehydration/skip.
	var extractClipsCalled bool
	extractClipsStep := &stateAssertingStep{
		name: "stock.extract_clips",
		assertFn: func(state *RunState) error {
			extractClipsCalled = true
			if len(state.Plan) != 1 {
				return fmt.Errorf("expected plan to survive through stage_sources, got %d", len(state.Plan))
			}
			return nil
		},
	}

	dispatchSteps := []Step{
		&stubRecorderStep{name: "stock.plan", count: new(int32)},
		stageSourcesStep,
		extractClipsStep,
		&stubRecorderStep{name: "stock.compose_chunks", count: new(int32)},
		&stubRecorderStep{name: "stock.publish", count: new(int32)},
	}

	cfg := OrchestratorConfig{JobId: jobID, StepStore: store}
	o := NewTestStockOrchestrator(cfg, resumeStubPlanner{}, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
	o.dispatchSteps = dispatchSteps

	_, err := o.RunResilient(ctx, &RunInput{})
	require.NoError(t, err)

	require.True(t, stageSourcesCalled, "stock.stage_sources must run and assert rehydrated state")
	require.True(t, extractClipsCalled, "stock.extract_clips must run with rehydrated state intact")

	// stock.plan was pre-completed and should not have re-run.
	rows, listErr := store.ListByJob(ctx, jobID)
	require.NoError(t, listErr)
	require.Equal(t, 5, len(rows), "one row per dispatch step")
}

// stateAssertingStep is a test Step that runs an custom assertion
// against the current RunState. It fails the run if the assertion
// returns an error.
type stateAssertingStep struct {
	name     string
	assertFn func(state *RunState) error
}

func (s *stateAssertingStep) Name() string { return s.name }
func (s *stateAssertingStep) Run(_ context.Context, runner StepRunner) error {
	return s.assertFn(runner.State())
}

// TestOrchestrator_RunResilient_PersistsRunState verifies that
// MarkCompleted persists the full RunState snapshot produced by a
// step. A step mutates Plan; after RunResilient we read the
// stock.plan row and assert its result_json contains the mutation.
func TestOrchestrator_RunResilient_PersistsRunState(t *testing.T) {
	db := openOrchestratorResumeTestDB(t)
	store := steps.NewSQLiteStoreWithDB(db)
	ctx := context.Background()
	jobID := "persist-state-test-1"

	mutatingStep := &stateMutatingStep{
		name: "stock.plan",
		mutateFn: func(state *RunState) {
			state.Plan = []ClipPlan{
				{SourceID: "https://example.com/video.mp4", OutputLogicalID: "planner:persist:0"},
			}
		},
	}

	dispatchSteps := []Step{
		mutatingStep,
		&stubRecorderStep{name: "stock.stage_sources", count: new(int32)},
		&stubRecorderStep{name: "stock.extract_clips", count: new(int32)},
		&stubRecorderStep{name: "stock.compose_chunks", count: new(int32)},
		&stubRecorderStep{name: "stock.publish", count: new(int32)},
	}

	cfg := OrchestratorConfig{JobId: jobID, StepStore: store}
	o := NewTestStockOrchestrator(cfg, resumeStubPlanner{}, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
	o.dispatchSteps = dispatchSteps

	_, err := o.RunResilient(ctx, &RunInput{})
	require.NoError(t, err)

	rows, listErr := store.ListByJob(ctx, jobID)
	require.NoError(t, listErr)

	var planRow *steps.StepState
	for i := range rows {
		if rows[i].StepKey == "stock.plan" {
			planRow = &rows[i]
			break
		}
	}
	require.NotNil(t, planRow, "stock.plan row must exist")
	require.True(t, len(planRow.Result) > 0, "stock.plan result_json must be non-empty")
	var checkpointEnvelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(planRow.Result, &checkpointEnvelope))
	require.Equal(t, `1`, string(checkpointEnvelope["checkpoint_version"]),
		"new checkpoints must carry the current checkpoint_version")

	var persisted RunState
	require.NoError(t, json.Unmarshal(planRow.Result, &persisted))
	require.Equal(t, 1, len(persisted.Plan), "persisted Plan must contain one entry")
	require.Equal(t, "https://example.com/video.mp4", persisted.Plan[0].SourceID)
}

// TestOrchestrator_RunResilient_RehydratesMultipleSteps verifies that
// when several consecutive steps are pre-completed, each step's
// checkpoint is rehydrated in pipeline order and the surviving state
// is the one from the latest pre-completed step.
func TestOrchestrator_RunResilient_RehydratesMultipleSteps(t *testing.T) {
	db := openOrchestratorResumeTestDB(t)
	store := steps.NewSQLiteStoreWithDB(db)
	ctx := context.Background()
	jobID := "rehydrate-multi-test-1"

	planState := RunState{
		Plan: []ClipPlan{{SourceID: "https://example.com/plan.mp4", OutputLogicalID: "planner:multi:0"}},
	}
	stageState := RunState{
		Plan: []ClipPlan{{SourceID: "https://example.com/plan.mp4", OutputLogicalID: "planner:multi:0"}},
		StagedAssets: []*assets.StagedAsset{
			{LocalPath: "/tmp/staged_multi.mp4", SourceID: "https://example.com/plan.mp4", Bytes: 1234},
		},
	}

	planKey := steps.StepKey{JobID: jobID, StepKey: "stock.plan", InputFingerprint: legacyStepInputFingerprint(jobID, "stock.plan")}
	stageKey := steps.StepKey{JobID: jobID, StepKey: "stock.stage_sources", InputFingerprint: legacyStepInputFingerprint(jobID, "stock.stage_sources")}

	planBytes, _ := json.Marshal(planState)
	stageBytes, _ := json.Marshal(stageState)

	require.NoError(t, store.MarkStarted(ctx, planKey))
	require.NoError(t, store.MarkCompleted(ctx, planKey, planBytes, nil))
	require.NoError(t, store.MarkStarted(ctx, stageKey))
	require.NoError(t, store.MarkCompleted(ctx, stageKey, stageBytes, nil))

	var extractCalled bool
	extractStep := &stateAssertingStep{
		name: "stock.extract_clips",
		assertFn: func(state *RunState) error {
			extractCalled = true
			if len(state.Plan) != 1 {
				return fmt.Errorf("expected 1 plan entry, got %d", len(state.Plan))
			}
			if len(state.StagedAssets) != 1 {
				return fmt.Errorf("expected 1 staged asset from stock.stage_sources checkpoint, got %d", len(state.StagedAssets))
			}
			if state.StagedAssets[0].LocalPath != "/tmp/staged_multi.mp4" {
				return fmt.Errorf("unexpected staged local path: %s", state.StagedAssets[0].LocalPath)
			}
			return nil
		},
	}

	planCount := new(int32)
	stageCount := new(int32)
	dispatchSteps := []Step{
		&stubRecorderStep{name: "stock.plan", count: planCount},
		&stubRecorderStep{name: "stock.stage_sources", count: stageCount},
		extractStep,
		&stubRecorderStep{name: "stock.compose_chunks", count: new(int32)},
		&stubRecorderStep{name: "stock.publish", count: new(int32)},
	}

	cfg := OrchestratorConfig{JobId: jobID, StepStore: store}
	o := NewTestStockOrchestrator(cfg, resumeStubPlanner{}, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
	o.dispatchSteps = dispatchSteps

	_, err := o.RunResilient(ctx, &RunInput{})
	require.NoError(t, err)
	require.True(t, extractCalled, "stock.extract_clips must run with rehydrated state")
	assert.Equal(t, int32(0), atomic.LoadInt32(planCount), "pre-completed stock.plan must not re-run")
	assert.Equal(t, int32(0), atomic.LoadInt32(stageCount), "pre-completed stock.stage_sources must not re-run")
}

// stateMutatingStep is a test Step that mutates RunState via a
// callback. Used to verify that MarkCompleted persists the mutated
// state.
