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
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	_ "github.com/mattn/go-sqlite3" // driver lock per AGENTS.md

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
	asset "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openOrchestratorResumeTestDB returns a hermetic SQLite DB with
// the execution_steps schema (migrations 121 + 122) applied inline.
// Inline (not importing the steps_test.go helper) so the
// orchestrator-test surface stays self-sufficient.
func openOrchestratorResumeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Inline schema: 121_execution_steps.sql + 122_execution_steps_add_lease_until.sql.
	// lease_until is declared inline (rather than via ALTER TABLE)
	// because the test DB starts fresh — no need for the incremental
	// migration shape; the production migration is the canonical
	// surface, this is a parallel test-only definition that mirrors
	// it.
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS execution_steps (
		    id INTEGER PRIMARY KEY AUTOINCREMENT,
		    job_id TEXT NOT NULL,
		    step_key TEXT NOT NULL,
		    input_fingerprint TEXT NOT NULL,
		    status TEXT NOT NULL DEFAULT 'pending',
		    attempt INTEGER NOT NULL DEFAULT 0,
		    result_json TEXT NOT NULL DEFAULT '{}',
		    artifact_refs_json TEXT NOT NULL DEFAULT '[]',
		    started_at TEXT NOT NULL DEFAULT '',
		    completed_at TEXT NOT NULL DEFAULT '',
		    last_error TEXT NOT NULL DEFAULT '',
		    lease_until TEXT NOT NULL DEFAULT ''
		);
		CREATE UNIQUE INDEX IF NOT EXISTS uniq_execution_steps_dedup
		    ON execution_steps (job_id, step_key, input_fingerprint);
		CREATE INDEX IF NOT EXISTS ix_execution_steps_resume
		    ON execution_steps (job_id, status, step_key);
		CREATE INDEX IF NOT EXISTS ix_execution_steps_audit
		    ON execution_steps (job_id, step_key);
		CREATE INDEX IF NOT EXISTS ix_execution_steps_leased_stale
		    ON execution_steps (lease_until)
		    WHERE lease_until != '';
	`)
	require.NoError(t, err, "openOrchestratorResumeTestDB: apply inline schema")
	return db
}

// stubRecorderStep is a Step impl that atomically counts invocations
// and returns nil. Used to verify which stages the orchestrator's
// resume contract chose to skip vs re-run.
type stubRecorderStep struct {
	name  string
	count *int32
}

func (s *stubRecorderStep) Name() string { return s.name }
func (s *stubRecorderStep) Run(_ context.Context, _ StepRunner) error {
	atomic.AddInt32(s.count, 1)
	return nil
}

// stubRecorderStepThrowing is a Step impl that records invocations and
// ALWAYS returns error. Used to verify the orchestrator's failure-path
// behaviour after a resume skip.
type stubRecorderStepThrowing struct {
	name  string
	count *int32
}

func (s *stubRecorderStepThrowing) Name() string { return s.name }
func (s *stubRecorderStepThrowing) Run(_ context.Context, _ StepRunner) error {
	atomic.AddInt32(s.count, 1)
	return assertErrRun
}

// assertErrRun is the canonical sentinel error the throwing stub
// returns. Tests assert errors.Is(err, assertErrRun) for the orchestrator's
// failure-path surface.
var assertErrRun = errRunStub("stub: run failed for test")

type errRunStub string

func (e errRunStub) Error() string { return string(e) }

// resumeStubPlanner satisfies the orchestrator's pre-loop nil-guard.
// Distinguishing prefix `resume` avoids name clashes with the
// stubPlanner declared in run_upload_indexing_test.go (same package).
type resumeStubPlanner struct{}

func (resumeStubPlanner) Plan(_ context.Context, _ VideoSource, _, _ int, _ string) ([]ClipPlan, error) {
	return nil, nil
}

// resumeStubStager satisfies the orchestrator's pre-loop nil-guard.
// Same rationale as resumeStubPlanner — renamed to avoid a
// redeclaration compile error against run_upload_indexing_test.go's
// `type stubStager struct{}` block (Go does NOT permit duplicate type
// declarations within a single package even across _test.go files).
type resumeStubStager struct{}

func (resumeStubStager) StageSource(_ context.Context, _ assets.SourceRef) (*assets.StagedAsset, error) {
	return nil, nil
}
func (resumeStubStager) StageSourceV2(_ context.Context, _ asset.SourceRef) (*asset.StagedSource, error) {
	return nil, nil
}
func (resumeStubStager) CleanupStagedSource(_ context.Context, _ *asset.StagedSource) error {
	return nil
}
func (resumeStubStager) Cleanup(_ context.Context, _ *assets.StagedAsset) error { return nil }

// TestOrchestrator_RunResilient_SkipAlreadyCompleted verifies the
// Step 10 C2/4 resume contract for the partial-progress case:
//   - Pre-Complete 2 of 5 stages in the steps.Store (simulating a
//     prior SIGKILL'd run that persisted progress before crash)
//   - RunResilient iterates all 5 dispatchSteps in pipeline order
//   - For each step, MarkStarted is called; ErrStepAlreadyCompleted
//     is returned for the 2 pre-Completed rows
//   - On ErrStepAlreadyCompleted, the orchestrator `continue`s to
//     the next step WITHOUT invoking the step's Run body and
//     WITHOUT calling MarkCompleted (terminal-immutability)
//   - The 3 non-pre-Completed steps Run their bodies + get
//     MarkCompleted (now 5 Completed rows total in the steps.Store)
func TestOrchestrator_RunResilient_SkipAlreadyCompleted(t *testing.T) {
	db := openOrchestratorResumeTestDB(t)
	store := steps.NewSQLiteStoreWithDB(db)
	ctx := context.Background()
	jobID := "resume-test-1"

	// Pre-Complete 2 of 5 stages — simulates a prior crashed run
	// that persisted progress to SQLite before SIGKILL.
	for _, name := range []string{"stock.plan", "stock.stage_sources"} {
		k := steps.StepKey{
			JobID:            jobID,
			StepKey:          name,
			InputFingerprint: stepInputFingerprint(jobID, name),
		}
		require.NoError(t, store.MarkStarted(ctx, k),
			"pre-Complete %q: MarkStarted", name)
		require.NoError(t, store.MarkCompleted(ctx, k, nil, nil),
			"pre-Complete %q: MarkCompleted", name)
	}

	// Build 5 stub recorder steps + atomic counters.
	counters := [5]*int32{}
	for i := range counters {
		counters[i] = new(int32)
	}
	dispatchSteps := []Step{
		&stubRecorderStep{name: "stock.plan", count: counters[0]},
		&stubRecorderStep{name: "stock.stage_sources", count: counters[1]},
		&stubRecorderStep{name: "stock.extract_clips", count: counters[2]},
		&stubRecorderStep{name: "stock.compose_chunks", count: counters[3]},
		&stubRecorderStep{name: "stock.publish", count: counters[4]},
	}

	cfg := OrchestratorConfig{
		JobId:     jobID,
		StepStore: store, // C2/4: inject SQLite-backed store via OrchestratorConfig
	}
	o := NewOrchestrator(cfg, resumeStubPlanner{}, nil, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
	o.dispatchSteps = dispatchSteps

	_, err := o.RunResilient(ctx, &RunInput{})
	require.NoError(t, err,
		"RunResilient should succeed with stub dispatchSteps (no errors thrown)")

	// Pre-Completed stages SKIPPED via ErrStepAlreadyCompleted on MarkStarted.
	assert.Equal(t, int32(0), atomic.LoadInt32(counters[0]),
		"stock.plan was pre-Completed; Run MUST NOT be called")
	assert.Equal(t, int32(0), atomic.LoadInt32(counters[1]),
		"stock.stage_sources was pre-Completed; Run MUST NOT be called")

	// NEW (non-pre-Completed) stages WERE called.
	assert.Equal(t, int32(1), atomic.LoadInt32(counters[2]),
		"stock.extract_clips was NEW; Run MUST be called")
	assert.Equal(t, int32(1), atomic.LoadInt32(counters[3]),
		"stock.compose_chunks was NEW; Run MUST be called")
	assert.Equal(t, int32(1), atomic.LoadInt32(counters[4]),
		"stock.publish was NEW; Run MUST be called")

	// stepStore has exactly 5 rows (one per dispatchStep; "no duplicate
	// stage rows" per user-spec acceptance — drive publishes SELECT count
	// invariant via ListByJob).
	rows, listErr := store.ListByJob(ctx, jobID)
	require.NoError(t, listErr)
	require.Equal(t, 5, len(rows),
		"stepStore has 5 rows: 2 pre-Completed + 3 newly Completed (no duplicates)")

	// All 5 rows are Completed (terminal state) — the orchestrator's
	// recovery contract completes the run end-to-end.
	for _, r := range rows {
		assert.Equal(t, steps.StatusCompleted, r.Status,
			"every row must be Completed after RunResilient: step=%s status=%s",
			r.StepKey, r.Status)
	}

	// Pre-Completed stages have attempt=1 (CAS-preserved by
	// ON CONFLICT CASE clause; re-MarkStarted does NOT bump attempt
	// when prior row was completed).
	// Newly-Completed stages are also at attempt=1 (fresh MarkStarted).
	for _, r := range rows {
		assert.Equal(t, 1, r.Attempt,
			"every row stays at attempt=1 in the resume contract: step=%s",
			r.StepKey)
	}
}

// TestOrchestrator_RunResilient_AllPreCompletedSkipsAll verifies the
// terminal-resume contract: when ALL dispatchSteps are pre-completed
// in the steps.Store, RunResilient still iterates the slice but
// skips every step's Run via ErrStepAlreadyCompleted.
func TestOrchestrator_RunResilient_AllPreCompletedSkipsAll(t *testing.T) {
	db := openOrchestratorResumeTestDB(t)
	store := steps.NewSQLiteStoreWithDB(db)
	ctx := context.Background()
	jobID := "all-completed-test"

	// Pre-complete ALL 5 stages.
	allNames := []string{
		"stock.plan",
		"stock.stage_sources",
		"stock.extract_clips",
		"stock.compose_chunks",
		"stock.publish",
	}
	for _, name := range allNames {
		k := steps.StepKey{
			JobID:            jobID,
			StepKey:          name,
			InputFingerprint: stepInputFingerprint(jobID, name),
		}
		require.NoError(t, store.MarkStarted(ctx, k))
		require.NoError(t, store.MarkCompleted(ctx, k, nil, nil))
	}

	// Stub dispatchSteps (Run should never be called).
	counters := [5]*int32{}
	for i := range counters {
		counters[i] = new(int32)
	}
	dispatchSteps := []Step{
		&stubRecorderStep{name: "stock.plan", count: counters[0]},
		&stubRecorderStep{name: "stock.stage_sources", count: counters[1]},
		&stubRecorderStep{name: "stock.extract_clips", count: counters[2]},
		&stubRecorderStep{name: "stock.compose_chunks", count: counters[3]},
		&stubRecorderStep{name: "stock.publish", count: counters[4]},
	}

	cfg := OrchestratorConfig{JobId: jobID, StepStore: store}
	o := NewOrchestrator(cfg, resumeStubPlanner{}, nil, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
	o.dispatchSteps = dispatchSteps

	_, err := o.RunResilient(ctx, &RunInput{})
	require.NoError(t, err)

	// Every step was pre-Completed; every Run was SKIPPED.
	for i, c := range counters {
		assert.Equal(t, int32(0), atomic.LoadInt32(c),
			"step[%d] %q was pre-Completed; Run MUST NOT be called", i, allNames[i])
	}

	// StepStore state remains correct (5 rows, all Completed at
	// attempt=1; the orchestrator's re-MarkStarted on each CAS-
	// preserved the prior values per the SQLite impl's CASE clause
	// in the UPSERT).
	rows, listErr := store.ListByJob(ctx, jobID)
	require.NoError(t, listErr)
	require.Equal(t, 5, len(rows))
	for _, r := range rows {
		assert.Equal(t, 1, r.Attempt,
			"all-pre-Completed stage %q stays at attempt=1 (CAS preserved)", r.StepKey)
		assert.Equal(t, steps.StatusCompleted, r.Status,
			"all-pre-Completed stage %q remains Completed", r.StepKey)
	}
}

// TestOrchestrator_RunResilient_NewStepFailureMarkFailed verifies the
// failure-path contract: a NEW (non-pre-Completed) step that throws
// on Run causes the orchestrator to:
//   - MarkFailed on the steps.Store row
//   - Return the original error wrapped with the step name
//   - NOT iterate subsequent steps (no post-failure recovery)
//
// This pins the godlike/07 fail-closed abort signal (non-nil Run
// return ⇒ MarkFailed + abort).
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
			InputFingerprint: stepInputFingerprint(jobID, name),
		}
		require.NoError(t, store.MarkStarted(ctx, k))
		require.NoError(t, store.MarkCompleted(ctx, k, nil, nil))
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
	o := NewOrchestrator(cfg, resumeStubPlanner{}, nil, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
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
		InputFingerprint: stepInputFingerprint(jobID, "stock.plan"),
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
	o := NewOrchestrator(cfg, resumeStubPlanner{}, nil, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
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
	o := NewOrchestrator(cfg, resumeStubPlanner{}, nil, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
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

	planKey := steps.StepKey{JobID: jobID, StepKey: "stock.plan", InputFingerprint: stepInputFingerprint(jobID, "stock.plan")}
	stageKey := steps.StepKey{JobID: jobID, StepKey: "stock.stage_sources", InputFingerprint: stepInputFingerprint(jobID, "stock.stage_sources")}

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
	o := NewOrchestrator(cfg, resumeStubPlanner{}, nil, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
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
type stateMutatingStep struct {
	name     string
	mutateFn func(state *RunState)
}

func (s *stateMutatingStep) Name() string { return s.name }
func (s *stateMutatingStep) Run(_ context.Context, runner StepRunner) error {
	s.mutateFn(runner.State())
	return nil
}

// TestOrchestrator_RunResilient_EmptyResultResumesBackwardCompatible
// verifies that a pre-completed step with an empty checkpoint
// result does not abort the run. This preserves backward
// compatibility with rows completed before state persistence was
// introduced; downstream steps simply see the empty accumulator.
func TestOrchestrator_RunResilient_EmptyResultResumesBackwardCompatible(t *testing.T) {
	db := openOrchestratorResumeTestDB(t)
	store := steps.NewSQLiteStoreWithDB(db)
	ctx := context.Background()
	jobID := "empty-result-test-1"

	planKey := steps.StepKey{
		JobID:            jobID,
		StepKey:          "stock.plan",
		InputFingerprint: stepInputFingerprint(jobID, "stock.plan"),
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
	o := NewOrchestrator(cfg, resumeStubPlanner{}, nil, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
	o.dispatchSteps = dispatchSteps

	_, err := o.RunResilient(ctx, &RunInput{})
	require.NoError(t, err)
	require.True(t, stageSourcesCalled, "stock.stage_sources must run after empty-result resume")
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
		InputFingerprint: stepInputFingerprint(jobID, "stock.plan"),
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
	o := NewOrchestrator(cfg, resumeStubPlanner{}, nil, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
	o.dispatchSteps = dispatchSteps

	_, err := o.RunResilient(ctx, &RunInput{})
	require.Error(t, err, "RunResilient must fail when a completed step has malformed checkpoint data")
	require.ErrorIs(t, err, ErrStockResumeStateInvalid, "error must wrap ErrStockResumeStateInvalid")
}
