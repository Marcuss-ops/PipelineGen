// Package stockpipeline — resume_test.go (DoD §10 server-restart
// recovery, July 2026).
//
// Pins the recovery contract for stock-pipeline jobs when the
// server is restarted mid-RUNNING:
//
//   - The steps.Store is the durable source-of-truth: a fresh
//     orchestrator constructed post-restart (separate Worker, same
//     DB) re-runs only the steps that were NEW (not pre-Completed)
//     in the prior run's steps.Store.
//   - The orchestrator's stepStore CAS (per the canonical
//     ON CONFLICT (job_id, step_key, input_fingerprint) DO UPDATE
//     in steps.SQLiteStore.Upsert) preserves pre-Completed rows'
//     Completed status + result_json + attempt counter.
//   - The orchestrator's MarkStarted returns
//     steps.ErrStepAlreadyCompleted for pre-Completed rows; the
//     per-step `continue` path skips the Run + post-Run MarkCompleted
//     so the stepStore row count is exactly len(dispatchSteps)
//     (no duplicate stage rows).
//
// What this test covers:
//
//  1. TestOrchestrator_PostRestart_StepStoreHasNoDuplicateRows
//     — fresh orchestrator, pre-Complete 4 of 5 stages, run, assert
//     exactly 5 rows in stepStore (no duplicates after retry).
//  2. TestOrchestrator_PostRestart_AllStepsCASPreserveAttempt1
//     — fresh orchestrator, pre-Complete 2 stages WITH attempt=2
//     set by store.MarkStarted incrementing under the hood, run,
//     assert all 5 rows have attempt=1 (CAS pinners intact).
//  3. TestOrchestrator_PostRestart_ListRowCountMatchesDispatchSlice
//     — diagnostic invariant (COUNT(*) == len(dispatchSteps)) that
//     operators use to confirm "no duplicate stage rows" via
//     SELECT COUNT(*) per the user-spec acceptance.
//
// Why this is a separate file from orchestrator_resume_test.go:
// the latter pins the CAS contract in general; this file frames
// the user-spec scenario explicitly (post-restart recovery) so the
// intent is clear in the test name. Both files share the
// `openOrchestratorResumeTestDB` helper declared in
// orchestrator_resume_test.go (same package).
package stockpipeline

import (
	"context"
	"sync/atomic"
	"testing"

	_ "github.com/mattn/go-sqlite3" // driver lock per AGENTS.md

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
)

// ── Test 1: no duplicate step rows after restart ───────────────────

// TestOrchestrator_PostRestart_StepStoreHasNoDuplicateRows pins the
// DoD §10 "no duplicates" invariant: a fresh orchestrator built
// against the SAME durable stepStore (post-restart scenario) sees
// pre-Completed rows from the prior run and re-runs only the new
// steps. The stepStore row count is exactly len(dispatchSteps)
// after RunResilient — no duplicates, no missed skips.
func TestOrchestrator_PostRestart_StepStoreHasNoDuplicateRows(t *testing.T) {
	t.Parallel()

	db := openOrchestratorResumeTestDB(t)
	store := steps.NewSQLiteStoreWithDB(db)
	jobID := "post-restart-no-dup-1"

	// Simulate prior progress: 4 of 5 stages pre-Completed by
	// the prior worker before crash.
	prestageNames := []string{
		"stock.plan",
		"stock.stage_sources",
		"stock.extract_clips",
		"stock.compose_chunks",
	}
	for _, name := range prestageNames {
		k := steps.StepKey{
			JobID:            jobID,
			StepKey:          name,
			InputFingerprint: legacyStepInputFingerprint(jobID, name),
		}
		require.NoError(t, store.MarkStarted(context.Background(), k))
		require.NoError(t, store.MarkCompleted(context.Background(), k, nil, nil),
			"pre-Complete %s", name)
	}

	// "Restart" simulation: a fresh orchestrator is built against
	// the SAME stepStore (so it sees the pre-Completed rows).
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

	_, err := o.RunResilient(context.Background(), &RunInput{})
	require.NoError(t, err)

	// Run invocation counts: 4 pre-Completed -> Skip, 1 new (publish) -> Run.
	for i := 0; i < 4; i++ {
		assert.Equal(t, int32(0), atomic.LoadInt32(counters[i]),
			"pre-Completed step[%d] %s MUST NOT re-run", i, prestageNames[i])
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(counters[4]),
		"only stock.publish (the new step) must re-run after restart")

	// stepStore row count = exactly len(dispatchSteps) = 5. This is
	// the canonical user-spec acceptance ("no duplicates" exactly).
	rows, listErr := store.ListByJob(context.Background(), jobID)
	require.NoError(t, listErr)
	require.Equal(t, 5, len(rows),
		"stepStore row count must equal dispatchSteps length (no duplicates after restart)")

	// All 5 rows are Completed (terminal-immutability under restart).
	for _, r := range rows {
		assert.Equal(t, steps.StatusCompleted, r.Status,
			"every row must be Completed post-RunResilient: step=%s status=%s",
			r.StepKey, r.Status)
	}
}

// ── Test 2: CAS preserves pre-Completed rows' attempt=1 ────────────

// TestOrchestrator_PostRestart_AllStepsCASPreserveAttempt1 pins the
// per-step CAS contract under restart: pre-Completed rows are NOT
// re-bumped on the orchestrator's second MarkStarted pass. The
// canonical store impl has ON CONFLICT (job_id, step_key, ...)
// DO UPDATE SET status = excluded.status WHERE … (terminal-
// immutability); the attempt column is bumped only on novel rows.
//
// Why this matters: a naive re-run would increment attempt on every
// step, blowing past MaxRetries and forcing a DeadLetter transition.
// The CAS pin prevents this regression.
func TestOrchestrator_PostRestart_AllStepsCASPreserveAttempt1(t *testing.T) {
	t.Parallel()

	db := openOrchestratorResumeTestDB(t)
	store := steps.NewSQLiteStoreWithDB(db)
	jobID := "post-restart-cas-1"

	// Pre-Complete 2 stages; the orchestrator's `continue` path
	// skips them on second pass. Their attempt counter stays at 1.
	prestageNames := []string{
		"stock.plan",
		"stock.stage_sources",
	}
	for _, name := range prestageNames {
		k := steps.StepKey{
			JobID:            jobID,
			StepKey:          name,
			InputFingerprint: legacyStepInputFingerprint(jobID, name),
		}
		require.NoError(t, store.MarkStarted(context.Background(), k))
		require.NoError(t, store.MarkCompleted(context.Background(), k, nil, nil))
	}

	// Stub dispatchSteps (5 total).
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

	_, err := o.RunResilient(context.Background(), &RunInput{})
	require.NoError(t, err)

	// All 5 rows stay at attempt=1 (no second-pass bump on either
	// pre-Completed or newly-Completed rows).
	rows, listErr := store.ListByJob(context.Background(), jobID)
	require.NoError(t, listErr)
	require.Equal(t, 5, len(rows))
	for _, r := range rows {
		assert.Equal(t, 1, r.Attempt,
			"every row must be at attempt=1 post-restart-resilient (CAS pinners intact): step=%s",
			r.StepKey)
	}
}

// ── Test 3: ListRowCountMatchesDispatchSlice (diagnostic counter) ─

// TestOrchestrator_PostRestart_ListRowCountMatchesDispatchSlice is
// the canonical "no duplicate stage rows" oracle operators run via
// SELECT COUNT(*) from the SQLite stepStore after a job. The
// orchestrator must produce exactly len(dispatchSteps) rows on
// recovery, regardless of how many were pre-Completed.
//
// Pinning this invariant gives an operator a fixed SQL query that
// confirms post-restart recovery is clean:
//
//	SELECT COUNT(*) FROM execution_steps WHERE job_id = ?;
//	-- expected: len(dispatchSteps)
//
// If COUNT(*) > len(dispatchSteps), the orchestrator has a row-
// dup bug; if COUNT(*) < len(dispatchSteps), it has skipped a real
// step. Both regressions are caught here.
func TestOrchestrator_PostRestart_ListRowCountMatchesDispatchSlice(t *testing.T) {
	t.Parallel()

	db := openOrchestratorResumeTestDB(t)
	store := steps.NewSQLiteStoreWithDB(db)
	jobID := "post-restart-count-1"

	// Pre-Complete 1 stage; run the orchestrator; the operator's
	// SELECT COUNT(*) must return 5 (len(dispatchSteps)), not 6
	// (duplicate from re-MarkStarted) and not 4 (skipped step).
	prestageName := "stock.plan"
	k := steps.StepKey{
		JobID:            jobID,
		StepKey:          prestageName,
		InputFingerprint: legacyStepInputFingerprint(jobID, prestageName),
	}
	require.NoError(t, store.MarkStarted(context.Background(), k))
	require.NoError(t, store.MarkCompleted(context.Background(), k, nil, nil))

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

	_, err := o.RunResilient(context.Background(), &RunInput{})
	require.NoError(t, err)

	// The canonical operator diagnostic: SELECT COUNT(*).
	var count int
	require.NoError(t, db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM execution_steps WHERE job_id = ?`, jobID).Scan(&count))
	assert.Equal(t, len(dispatchSteps), count,
		"SELECT COUNT(*) must equal len(dispatchSteps) (no duplicate rows post-restart)")

	// Diagnostic: every step_key appears EXACTLY once. JOIN to
	// dispatchStep names to verify one-per-key.
	rows, listErr := db.QueryContext(context.Background(),
		`SELECT step_key, COUNT(*) FROM execution_steps WHERE job_id = ? GROUP BY step_key HAVING COUNT(*) > 1`,
		jobID)
	require.NoError(t, listErr)
	defer rows.Close()
	dupKeys := []string{}
	for rows.Next() {
		var k string
		var c int
		require.NoError(t, rows.Scan(&k, &c))
		dupKeys = append(dupKeys, k)
	}
	assert.Empty(t, dupKeys,
		"no step_key may have COUNT(*) > 1 in the stepStore post-restart; duplicates: %v", dupKeys)
}

// No package-level placeholders — `database/sql` and `path/filepath`
// are imported (transitively) via the sibling
// orchestrator_resume_test.go's helpers; we use only the typed
// surface (steps.StepState) here so no extra imports are needed.
