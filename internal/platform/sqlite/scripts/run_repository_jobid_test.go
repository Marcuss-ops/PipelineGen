// Package scripts — run_repository_jobid_test.go.
//
// Regression coverage for the ghost-run ledger: job_id is NOT unique in
// run_observability (the observability recorder writes its own attempt row per
// job), and the worker row's second-precision created_at sorts AFTER the
// submission row's nanosecond stamp as TEXT. An unfiltered
// `ORDER BY created_at DESC` therefore handed the durable lane the WORKER row,
// which it executed and finalised, leaving the submission run — the row GET
// /full and every operator surface read — RUNNING forever.
//
// The tests pin both halves of the contract:
//   - a submission row always wins, even when the worker row was written later
//     in the same second;
//   - a job with no submission row (internal jobs that never went through the
//     HTTP starter) keeps the historical correlation instead of returning nil.
package scripts

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// TestGetByJobIDPrefersTheSubmissionRunOverTheWorkerAttemptRow captures the
// production shape: both rows share job_id, and the worker row is written
// later with a second-precision timestamp that sorts last as TEXT.
func TestGetByJobIDPrefersTheSubmissionRunOverTheWorkerAttemptRow(t *testing.T) {
	ctx := context.Background()
	repo, db := newRunRepositoryTestRepo(t)

	const jobID = "job_1791633743485791483_1"
	submittedAt := time.Date(2026, 10, 10, 12, 2, 23, 27_707_553, time.UTC)
	require.NoError(t, repo.Create(ctx, &scriptgen.GenerationRun{
		ID:           "run_432dcc5b-eb4f-4a6a-9444-fe0182c1c7e2",
		JobID:        jobID,
		Request:      scriptgen.GenerateRequest{IdempotencyKey: "editorial-runtime-cert-001"},
		Status:       scriptgen.RunStatusPending,
		CurrentStage: scriptgen.StageNormalizing,
		CreatedAt:    submittedAt,
		UpdatedAt:    submittedAt,
	}))
	// The recorder's own attempt row for the same job, written later in the
	// same second with a second-precision stamp (the sort trap).
	_, err := db.ExecContext(ctx, `INSERT INTO run_observability
		(run_id,job_id,job_type,attempt_id,status,created_at,started_at,report_json,workflow_payload_json,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		"run_1791633743486814799_035634807d57", jobID, "script.generate",
		"attempt_1791633743486637708_0513e3a17908f9f7", "SUCCEEDED",
		"2026-10-10T12:02:23Z", "2026-10-10T12:02:23.486811026Z", "{}", "{}", "2026-10-10T12:04:22.729586875Z")
	require.NoError(t, err)

	run, err := repo.GetByJobID(ctx, jobID)
	require.NoError(t, err)
	require.NotNil(t, run, "the submission run must be found for its job")
	require.Equal(t, "run_432dcc5b-eb4f-4a6a-9444-fe0182c1c7e2", run.ID,
		"GetByJobID must return the submission run, not the worker attempt row")
	require.Equal(t, scriptgen.RunStatusPending, run.Status,
		"the submission checkpoint (not the worker row's status) describes the run")
	require.Equal(t, "editorial-runtime-cert-001", run.Request.IdempotencyKey)
}

// TestGetByJobIDFallsBackToTheJobRowWithoutASubmissionRun documents the second
// half: internal jobs that never went through the HTTP starter have no
// submission row, so the historical correlation is preserved rather than
// returning nil and silently dropping the run from the durable lane.
func TestGetByJobIDFallsBackToTheJobRowWithoutASubmissionRun(t *testing.T) {
	ctx := context.Background()
	repo, db := newRunRepositoryTestRepo(t)

	const jobID = "job_internal_only_1"
	_, err := db.ExecContext(ctx, `INSERT INTO run_observability
		(run_id,job_id,job_type,attempt_id,status,created_at,started_at,report_json,workflow_payload_json,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		"run_internal_worker_1", jobID, "script.generate", "attempt_internal_1", "RUNNING",
		"2026-10-10T12:00:00Z", "2026-10-10T12:00:00Z", "{}", "{}", "2026-10-10T12:00:00Z")
	require.NoError(t, err)

	run, err := repo.GetByJobID(ctx, jobID)
	require.NoError(t, err)
	require.NotNil(t, run)
	require.Equal(t, "run_internal_worker_1", run.ID)

	missing, err := repo.GetByJobID(ctx, "job_that_does_not_exist")
	require.NoError(t, err)
	require.Nil(t, missing)
}

func TestCreateRollsBackAttemptWhenRunCheckpointWriteFails(t *testing.T) {
	ctx := context.Background()
	repo, db := newRunRepositoryTestRepo(t)
	_, err := db.ExecContext(ctx, `CREATE TRIGGER fail_submission_run_insert
		BEFORE INSERT ON run_observability
		BEGIN SELECT RAISE(ABORT, 'injected run checkpoint failure'); END`)
	require.NoError(t, err)

	err = repo.Create(ctx, &scriptgen.GenerationRun{
		ID:           "run-create-atomic",
		Request:      scriptgen.GenerateRequest{IdempotencyKey: "create-atomic"},
		Status:       scriptgen.RunStatusPending,
		CurrentStage: scriptgen.StageNormalizing,
	})
	require.Error(t, err)

	var attempts, runs int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_attempts WHERE run_id=?`, "run-create-atomic").Scan(&attempts))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_observability WHERE run_id=?`, "run-create-atomic").Scan(&runs))
	require.Zero(t, attempts, "attempt insert must roll back when the run checkpoint cannot be written")
	require.Zero(t, runs)
}

func TestSetJobIDRollsBackAttemptWhenRunUpdateFails(t *testing.T) {
	ctx := context.Background()
	repo, db := newRunRepositoryTestRepo(t)
	const runID = "run-jobid-atomic"
	require.NoError(t, repo.Create(ctx, &scriptgen.GenerationRun{
		ID:           runID,
		Request:      scriptgen.GenerateRequest{IdempotencyKey: "jobid-atomic"},
		Status:       scriptgen.RunStatusPending,
		CurrentStage: scriptgen.StageNormalizing,
	}))
	_, err := db.ExecContext(ctx, `CREATE TRIGGER fail_submission_run_jobid
		BEFORE UPDATE OF job_id ON run_observability WHEN NEW.job_id='job-fail'
		BEGIN SELECT RAISE(ABORT, 'injected job id failure'); END`)
	require.NoError(t, err)

	err = repo.SetJobID(ctx, runID, "job-fail")
	require.Error(t, err)
	var attemptJobID, runJobID string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT job_id FROM job_attempts WHERE run_id=?`, runID).Scan(&attemptJobID))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT job_id FROM run_observability WHERE run_id=?`, runID).Scan(&runJobID))
	require.Equal(t, runID, attemptJobID, "attempt correlation must roll back with the failed run update")
	require.Equal(t, runID, runJobID)
}

func TestListStaleSubmissionRunsFindsOnlyOldNonterminalIntents(t *testing.T) {
	ctx := context.Background()
	repo, db := newRunRepositoryTestRepo(t)
	const staleRun = "run-stale-unlinked"
	const runningRun = "run-stale-running"
	const freshRun = "run-fresh-unlinked"
	for _, run := range []struct {
		id     string
		status scriptgen.RunStatus
		stage  scriptgen.Stage
	}{
		{id: staleRun, status: scriptgen.RunStatusPending, stage: scriptgen.StagePreflight},
		{id: runningRun, status: scriptgen.RunStatusRunning, stage: scriptgen.StageGeneratingSceneText},
		{id: freshRun, status: scriptgen.RunStatusPending, stage: scriptgen.StagePreflight},
	} {
		require.NoError(t, repo.Create(ctx, &scriptgen.GenerationRun{
			ID:           run.id,
			Request:      scriptgen.GenerateRequest{IdempotencyKey: "idem-" + run.id},
			Status:       run.status,
			CurrentStage: run.stage,
		}))
	}
	cutoff := time.Now().UTC().Add(-time.Hour)
	for _, runID := range []string{staleRun, runningRun} {
		_, err := db.ExecContext(ctx, `UPDATE run_observability SET updated_at=? WHERE run_id=?`, formatTime(cutoff.Add(-time.Minute)), runID)
		require.NoError(t, err)
	}

	candidates, err := repo.ListStaleSubmissionRuns(ctx, cutoff, 10)
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	byID := map[string]scriptgen.StaleSubmissionRun{}
	for _, candidate := range candidates {
		byID[candidate.RunID] = candidate
	}
	require.Equal(t, staleRun, byID[staleRun].JobID)
	require.Equal(t, "idem-"+staleRun, byID[staleRun].IdempotencyKey)
	require.Equal(t, scriptgen.StagePreflight, byID[staleRun].CurrentStage)
	require.Equal(t, scriptgen.StageGeneratingSceneText, byID[runningRun].CurrentStage)

	require.NoError(t, repo.SetJobID(ctx, staleRun, "job-linked"))
	candidates, err = repo.ListStaleSubmissionRuns(ctx, cutoff, 10)
	require.NoError(t, err)
	require.Len(t, candidates, 1, "recently checked linked runs wait another grace period")
	require.Equal(t, runningRun, candidates[0].RunID)
}
