// Package maintenance — reconcile_orphaned_runs_test.go.
//
// Coverage for the retention fallback: a RUNNING run whose canonical jobs row
// has been pruned must still be closable from the terminal attempt run for the
// same job_id. Without it, jobs retention converts every unfinalized run into a
// permanent ghost (the October 2026 ledger had 793 of them). The tests fail
// closed on the dangerous direction too: a job whose attempt is still running,
// or a run with no attempt at all, must stay RUNNING rather than be reported as
// finished.
package maintenance

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

const reconcileTestSchema = `
	CREATE TABLE run_observability (
		run_id                TEXT PRIMARY KEY,
		job_id                TEXT NOT NULL DEFAULT '',
		job_type              TEXT NOT NULL DEFAULT '',
		attempt_id            TEXT NOT NULL DEFAULT '',
		status                TEXT NOT NULL DEFAULT '',
		created_at            TEXT NOT NULL DEFAULT '',
		started_at            TEXT NOT NULL DEFAULT '',
		finished_at           TEXT,
		wall_time_ms          INTEGER NOT NULL DEFAULT 0,
		queue_wait_ms         INTEGER NOT NULL DEFAULT 0,
		report_json           TEXT NOT NULL DEFAULT '{}',
		error_code            TEXT NOT NULL DEFAULT '',
		error                 TEXT NOT NULL DEFAULT ''
	);
`

func newReconcileTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(reconcileTestSchema)
	require.NoError(t, err)
	return db
}

func insertRun(t *testing.T, db *sql.DB, runID, jobID, attemptID, status, finishedAt string, wallMs int64) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO run_observability
		(run_id,job_id,job_type,attempt_id,status,created_at,started_at,finished_at,wall_time_ms,report_json,error_code,error)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		runID, jobID, "script.generate", attemptID, status,
		"2026-10-10T12:02:23.027707553Z", "2026-10-10T12:02:23.027707553Z",
		finishedAt, wallMs, "{}", "", "")
	require.NoError(t, err)
}

// TestResolveAttemptEvidenceUsesTheTerminalAttemptRun pins the fallback: with
// the jobs row already pruned, the worker attempt row carries the outcome.
func TestResolveAttemptEvidenceUsesTheTerminalAttemptRun(t *testing.T) {
	ctx := context.Background()
	db := newReconcileTestDB(t)
	const jobID = "job_1791633743040525862_749deb92"
	// The submission row being repaired must never be its own evidence.
	insertRun(t, db, "run_432dcc5b", jobID, "run_432dcc5b:script", "RUNNING", "", 0)
	insertRun(t, db, "run_1791633743486814", jobID, "attempt_1791633743486637708", "SUCCEEDED",
		"2026-10-10T12:04:22.729586875Z", 119242)

	evidence, reason, err := resolveAttemptEvidence(ctx, db, jobID)
	require.NoError(t, err)
	require.Empty(t, reason)
	require.NotNil(t, evidence)
	require.Equal(t, kernobs.StatusSucceeded, evidence.status)
	require.Equal(t, "attempt", evidence.source)
	require.EqualValues(t, 119242, evidence.durationMs)
	require.Equal(t, time.Date(2026, 10, 10, 12, 4, 22, 729586875, time.UTC), evidence.finishedAt)
	require.Empty(t, evidence.errCode, "a succeeded attempt carries no error code")
}

// TestResolveAttemptEvidenceFailsClosedOnLiveOrMissingAttempts is the
// dangerous direction: an unsettled attempt, or none at all, must leave the
// run RUNNING instead of inventing a verdict.
func TestResolveAttemptEvidenceFailsClosedOnLiveOrMissingAttempts(t *testing.T) {
	ctx := context.Background()

	t.Run("attempt still running", func(t *testing.T) {
		db := newReconcileTestDB(t)
		const jobID = "job_live"
		insertRun(t, db, "run_submit_live", jobID, "run_submit_live:script", "RUNNING", "", 0)
		insertRun(t, db, "run_worker_live", jobID, "attempt_live", "RUNNING", "", 0)

		evidence, reason, err := resolveAttemptEvidence(ctx, db, jobID)
		require.NoError(t, err)
		require.Nil(t, evidence)
		require.Contains(t, reason, "no terminal attempt run")
	})

	t.Run("no attempt row at all", func(t *testing.T) {
		db := newReconcileTestDB(t)
		const jobID = "job_only_submission"
		insertRun(t, db, "run_submit_only", jobID, "run_submit_only:script", "RUNNING", "", 0)

		evidence, reason, err := resolveAttemptEvidence(ctx, db, jobID)
		require.NoError(t, err)
		require.Nil(t, evidence)
		require.Contains(t, reason, "no terminal attempt run")
	})
}

// TestResolveAttemptEvidencePrefersTheNewestTerminalAttempt pins that a retried
// job is closed with its latest outcome, not its first one.
func TestResolveAttemptEvidencePrefersTheNewestTerminalAttempt(t *testing.T) {
	ctx := context.Background()
	db := newReconcileTestDB(t)
	const jobID = "job_retried"
	insertRun(t, db, "run_attempt_old", jobID, "attempt_old", "FAILED", "2026-10-10T11:00:00Z", 100)
	insertRun(t, db, "run_attempt_new", jobID, "attempt_new", "SUCCEEDED", "2026-10-10T12:00:00Z", 200)

	evidence, _, err := resolveAttemptEvidence(ctx, db, jobID)
	require.NoError(t, err)
	require.NotNil(t, evidence)
	require.Equal(t, kernobs.StatusSucceeded, evidence.status)
	require.EqualValues(t, 200, evidence.durationMs)
}

// TestBuildFinalReportProjectsTheEvidence pins the projection onto the run row:
// identity comes from the run being repaired, the outcome from the evidence.
func TestBuildFinalReportProjectsTheEvidence(t *testing.T) {
	o := orphanRun{
		runID: "run_432dcc5b-eb4f-4a6a-9444-fe0182c1c7e2", jobID: "job_1",
		jobType: "script.generate", attemptID: "run_432dcc5b:script",
		createdAt: "2026-10-10T12:02:23.027707553Z", startedAt: "2026-10-10T12:02:23.027707553Z",
		reportJSON: "{}",
	}
	report, err := buildFinalReport(o, &jobEvidence{
		status: kernobs.StatusFailed, errCode: "ORPHANED_SUBMISSION_RUN", errMsg: "boom",
		finishedAt: time.Date(2026, 10, 10, 12, 4, 22, 0, time.UTC), durationMs: 42, source: "attempt",
	})
	require.NoError(t, err)
	require.Equal(t, o.runID, report.RunID)
	require.Equal(t, o.jobID, report.JobID)
	require.Equal(t, o.attemptID, report.AttemptID)
	require.Equal(t, kernobs.StatusFailed, report.Status)
	require.EqualValues(t, 42, report.WallTimeMs)
	require.Equal(t, "ORPHANED_SUBMISSION_RUN", report.ErrorCode)
	require.Equal(t, "boom", report.Error)

	succeeded, err := buildFinalReport(o, &jobEvidence{
		status: kernobs.StatusSucceeded, errCode: "stale", errMsg: "stale",
		finishedAt: time.Date(2026, 10, 10, 12, 4, 22, 0, time.UTC), durationMs: 7, source: "attempt",
	})
	require.NoError(t, err)
	require.Empty(t, succeeded.ErrorCode, "a succeeded run must not carry a stale error code")
	require.Empty(t, succeeded.Error)
}

// TestTerminalJobStatusRejectsLiveJobs pins which job states may close a run.
func TestTerminalJobStatusRejectsLiveJobs(t *testing.T) {
	for _, s := range []string{kernobs.StatusSucceeded, kernobs.StatusFailed, kernobs.StatusCancelled} {
		require.True(t, terminalJobStatus(s), "%s must be terminal", s)
	}
	for _, s := range []string{"RUNNING", "QUEUED", "PENDING", "WAITING", ""} {
		require.False(t, terminalJobStatus(s), "%s must not close a run", s)
	}
}
