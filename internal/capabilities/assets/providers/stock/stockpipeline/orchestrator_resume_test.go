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
	"path/filepath"
	"sync/atomic"
	"testing"

	_ "github.com/mattn/go-sqlite3" // driver lock per AGENTS.md

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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

func (resumeStubStager) Prepare(_ context.Context, _ acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	return nil, nil
}
func (resumeStubStager) Release(_ context.Context, _ string) error { return nil }

// failingResumeStore preserves the canonical Store surface while making
// the one resume read fail. Embedding delegates every mutation to the
// real store, so the test exercises RunResilient's read-failure handling
// rather than a second store implementation.
type failingResumeStore struct {
	steps.Store
	listErr error
}

func (s failingResumeStore) ListByJob(context.Context, string) ([]steps.StepState, error) {
	return nil, s.listErr
}

// inconsistentResumeStore simulates a store that reports a terminal
// step from MarkStarted while its history read cannot return that row.
// This is a defensive race/corruption seam: RunResilient must not skip
// the step without a canonical snapshot.
type inconsistentResumeStore struct {
	steps.Store
	completedKey steps.StepKey
}

func (s inconsistentResumeStore) MarkStarted(_ context.Context, key steps.StepKey) error {
	if key.JobID == s.completedKey.JobID && key.StepKey == s.completedKey.StepKey {
		return steps.ErrStepAlreadyCompleted
	}
	return s.Store.MarkStarted(context.Background(), key)
}

var _ steps.Store = inconsistentResumeStore{}

// TestOrchestrator_RunResilient_CheckpointReadFailsClosed verifies that
// an unreadable canonical checkpoint store aborts before any step runs.
