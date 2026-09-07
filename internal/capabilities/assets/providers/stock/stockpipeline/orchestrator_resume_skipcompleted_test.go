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
	"fmt"
	"sync/atomic"
	"testing"

	_ "github.com/mattn/go-sqlite3" // driver lock per AGENTS.md

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrchestrator_RunResilient_CheckpointReadFailsClosed(t *testing.T) {
	readErr := fmt.Errorf("checkpoint database unavailable")
	store := failingResumeStore{
		Store:   steps.NewInMemoryStore(),
		listErr: readErr,
	}
	count := new(int32)
	o := NewTestStockOrchestrator(
		OrchestratorConfig{JobId: "checkpoint-read-failure", StepStore: store},
		resumeStubPlanner{}, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{},
	)
	o.dispatchSteps = []Step{
		&stubRecorderStep{name: "stock.plan", count: count},
		&stubRecorderStep{name: "stock.stage_sources", count: new(int32)},
	}

	_, err := o.RunResilient(context.Background(), &RunInput{})
	require.Error(t, err, "checkpoint read failure must abort the run")
	require.ErrorIs(t, err, ErrStockResumeStateReadFailed)
	require.ErrorIs(t, err, readErr)
	require.Zero(t, atomic.LoadInt32(count), "no step may run when canonical state cannot be read")
}

// TestOrchestrator_RunResilient_CompletedWithoutReadableRowFailsClosed
// verifies that terminal state and readable checkpoint state are one
// contract: a completed marker without its row cannot authorize a skip.
func TestOrchestrator_RunResilient_CompletedWithoutReadableRowFailsClosed(t *testing.T) {
	completedKey := steps.StepKey{
		JobID:            "completed-row-missing",
		StepKey:          "stock.plan",
		InputFingerprint: legacyStepInputFingerprint("completed-row-missing", "stock.plan"),
	}
	base := steps.NewInMemoryStore()
	store := inconsistentResumeStore{Store: base, completedKey: completedKey}
	count := new(int32)
	o := NewTestStockOrchestrator(
		OrchestratorConfig{JobId: completedKey.JobID, StepStore: store},
		resumeStubPlanner{}, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{},
	)
	o.dispatchSteps = []Step{
		&stubRecorderStep{name: completedKey.StepKey, count: count},
		&stubRecorderStep{name: "stock.stage_sources", count: new(int32)},
	}

	_, err := o.RunResilient(context.Background(), &RunInput{})
	require.Error(t, err, "a completed marker without a readable row must abort resume")
	require.ErrorIs(t, err, ErrStockResumeStateReadFailed)
	require.Zero(t, atomic.LoadInt32(count), "the unrehydrated completed step must not run")
}

// TestLoadCompletedStepRows_DropsStaleCompletedWhenLatestAttemptFailed
// verifies that a newer failed fingerprint is not hidden by an older
// completed checkpoint. Retry must execute the latest failed attempt.
func TestOrchestrator_RunResilient_ResumesV2CheckpointDuringV3Migration(t *testing.T) {
	store := steps.NewInMemoryStore()
	ctx := context.Background()
	jobID := "resume-v2-migration"
	input := &RunInput{DirectURLs: []string{"https://example.com/source.mp4"}}
	cfg := OrchestratorConfig{JobId: jobID, PolicyVersion: "policy-v1", StepStore: store}
	stepName := "stock.plan"
	v2Fingerprint := legacyV2StepInputFingerprint(jobID, stepName, cfg, input, nil)
	key := steps.StepKey{JobID: jobID, StepKey: stepName, InputFingerprint: v2Fingerprint}
	require.NoError(t, store.MarkStarted(ctx, key))
	require.NoError(t, store.MarkCompleted(ctx, key, []byte(`{"checkpoint_version":1,"Plan":[]}`), nil))

	count := new(int32)
	o := NewTestStockOrchestrator(cfg, resumeStubPlanner{}, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
	o.dispatchSteps = []Step{
		&stubRecorderStep{name: stepName, count: count},
	}

	_, err := o.RunResilient(ctx, input)
	require.NoError(t, err)
	require.Zero(t, atomic.LoadInt32(count), "a matching v2 checkpoint must be resumed without rerunning the step")

	rows, listErr := store.ListByJob(ctx, jobID)
	require.NoError(t, listErr)
	require.Len(t, rows, 1, "v2 resume must not create a duplicate checkpoint row")
	require.Equal(t, v2Fingerprint, rows[0].Fingerprint)
}

func TestOrchestrator_RunResilient_DoesNotResumeMismatchedV2Checkpoint(t *testing.T) {
	store := steps.NewInMemoryStore()
	ctx := context.Background()
	jobID := "resume-v2-mismatch"
	input := &RunInput{DirectURLs: []string{"https://example.com/source.mp4"}}
	cfg := OrchestratorConfig{JobId: jobID, PolicyVersion: "policy-v1", StepStore: store}
	stepName := "stock.plan"
	key := steps.StepKey{JobID: jobID, StepKey: stepName, InputFingerprint: legacyV2StepInputFingerprint(jobID, stepName, cfg, &RunInput{DirectURLs: []string{"https://example.com/other.mp4"}}, nil)}
	require.NoError(t, store.MarkStarted(ctx, key))
	require.NoError(t, store.MarkCompleted(ctx, key, []byte(`{"checkpoint_version":1,"Plan":[]}`), nil))

	count := new(int32)
	o := NewTestStockOrchestrator(cfg, resumeStubPlanner{}, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
	o.dispatchSteps = []Step{&stubRecorderStep{name: stepName, count: count}}
	_, err := o.RunResilient(ctx, input)
	require.NoError(t, err)
	require.Equal(t, int32(1), atomic.LoadInt32(count), "a mismatched v2 checkpoint must not authorize a skip")
	rows, listErr := store.ListByJob(ctx, jobID)
	require.NoError(t, listErr)
	require.Len(t, rows, 2, "mismatched v2 input must create a new fingerprint version")
}

func TestLoadCompletedStepRows_DropsStaleCompletedWhenLatestAttemptFailed(t *testing.T) {
	db := openOrchestratorResumeTestDB(t)
	store := steps.NewSQLiteStoreWithDB(db)
	ctx := context.Background()
	jobID := "latest-failed-attempt"
	completedKey := steps.StepKey{JobID: jobID, StepKey: "stock.plan", InputFingerprint: "fp-completed"}
	failedKey := steps.StepKey{JobID: jobID, StepKey: "stock.plan", InputFingerprint: "fp-failed"}

	require.NoError(t, store.MarkStarted(ctx, completedKey))
	require.NoError(t, store.MarkCompleted(ctx, completedKey, []byte(`{"checkpoint_version":1,"Plan":[]}`), nil))
	require.NoError(t, store.MarkStarted(ctx, failedKey))
	require.NoError(t, store.MarkFailed(ctx, failedKey, "transient failure"))

	o := &Orchestrator{stepStore: store}
	completedRows, err := o.loadCompletedStepRows(ctx, jobID)
	require.NoError(t, err)
	_, exists := completedRows["stock.plan"]
	require.False(t, exists, "a newer Failed row must prevent stale Completed resume")
}

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
			InputFingerprint: legacyStepInputFingerprint(jobID, name),
		}
		require.NoError(t, store.MarkStarted(ctx, k),
			"pre-Complete %q: MarkStarted", name)
		require.NoError(t, store.MarkCompleted(ctx, k, []byte(`{"checkpoint_version":1,"Plan":[]}`), []byte(`[]`)),
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
	o := NewTestStockOrchestrator(cfg, resumeStubPlanner{}, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
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
			InputFingerprint: legacyStepInputFingerprint(jobID, name),
		}
		require.NoError(t, store.MarkStarted(ctx, k))
		require.NoError(t, store.MarkCompleted(ctx, k, []byte(`{"checkpoint_version":1,"Plan":[]}`), []byte(`[]`)))
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
	o := NewTestStockOrchestrator(cfg, resumeStubPlanner{}, resumeStubStager{}, fakeSucceedingCutter{}, noopRenderer{})
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
