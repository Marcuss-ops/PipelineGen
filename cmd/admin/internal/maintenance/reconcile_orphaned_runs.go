package maintenance

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	obsmetrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"go.uber.org/zap"
)

// runReconcileOrphanedRuns finalizes observability runs stuck in RUNNING
// whose job already reached a terminal state (SUCCEEDED/FAILED/CANCELLED).
//
// This is the one-time repair for the shutdown bug: before the detached-ctx
// fix (internal/kernel/observability/run.go emit) and the lease-fence
// population (ClaimRunInfo), a worker that died mid-finalize left both
// run_observability and job_attempts in RUNNING forever. RecoverAbandoned
// cannot touch them because their lease_expires_at is NULL (the lease is
// only populated for NEW claims), so this command reconciles them directly.
//
// For each RUNNING run it resolves the terminal outcome from two evidence
// sources, in order:
//
//  1. the canonical job (jobs plane) — the job's status/duration/completed_at
//     are projected onto the run;
//  2. the terminal attempt run for the same job_id in run_observability —
//     jobs retention prunes terminal job rows, and after that the attempt run
//     is the only remaining execution-plane evidence. Without this fallback
//     the prune turns every unfinalized run into a permanent ghost (793
//     script.generate runs, all pruned, in the October 2026 ledger).
//
// The run is finalized through the canonical SQLiteRecorder.SaveReport path
// (so run_observability + job_attempts + report_json are updated atomically)
// and inherits the status of its evidence — NOT ABANDONED: a job that
// succeeded must not be reported as worker-lost. A run whose job is still
// live (not terminal) is left RUNNING on purpose.
//
//	admin reconcile-orphaned-runs            # dry-run (report only)
//	admin reconcile-orphaned-runs --apply    # write the reconciliation
func RunReconcileOrphanedRuns(args []string) error {
	fs := flag.NewFlagSet("reconcile-orphaned-runs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "Apply the reconciliation; default is a dry-run report")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	dbSet, err := cli.OpenDatabaseSet(cfg, log)
	if err != nil {
		return fmt.Errorf("open database set: %w", err)
	}
	defer dbSet.Close()

	ctx := context.Background()
	rec := obsmetrics.NewSQLiteRecorderWithLogger(dbSet.Observability.DB, log)

	// The canonical `jobs` table lives in the execution plane when the jobs
	// split is enabled (jobs.split_db_enabled=true, the production layout) and
	// in Primary only in the single-file legacy layout. Reading the wrong one
	// aborts the whole repair with "no such table: jobs", which is exactly how
	// the ghost-run ledger stayed unreconciled.
	jobDB := dbSet.Primary.DB
	if dbSet.Jobs != nil {
		jobDB = dbSet.Jobs.DB
	}

	rows, err := dbSet.Observability.DB.QueryContext(ctx,
		`SELECT run_id, job_id, job_type, attempt_id, created_at, started_at, queue_wait_ms, report_json
		 FROM run_observability
		 WHERE status = 'RUNNING'
		 ORDER BY started_at ASC`)
	if err != nil {
		return fmt.Errorf("select running runs: %w", err)
	}
	defer rows.Close()

	var orphans []orphanRun
	for rows.Next() {
		var o orphanRun
		var createdAt, startedAt sql.NullString
		if err := rows.Scan(&o.runID, &o.jobID, &o.jobType, &o.attemptID, &createdAt, &startedAt, &o.queueWaitMs, &o.reportJSON); err != nil {
			return fmt.Errorf("scan running run: %w", err)
		}
		o.createdAt, o.startedAt = createdAt.String, startedAt.String
		orphans = append(orphans, o)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate running runs: %w", err)
	}

	var reconciled, skipped int
	var byStatus = map[string]int{}
	for _, o := range orphans {
		evidence, reason, err := resolveJobEvidence(ctx, jobDB, dbSet.Observability.DB, o)
		if err != nil {
			return err
		}
		if evidence == nil {
			skipped++
			log.Warn("orphaned run has no terminal evidence; skipped",
				zap.String("run_id", o.runID), zap.String("job_id", o.jobID), zap.String("reason", reason))
			continue
		}

		report, err := buildFinalReport(o, evidence)
		if err != nil {
			skipped++
			log.Warn("build final report failed; skipped",
				zap.String("run_id", o.runID), zap.Error(err))
			continue
		}

		line := fmt.Sprintf("%-48s job=%-12s -> %s (wall=%dms source=%s)", o.runID, o.jobID, evidence.status, evidence.durationMs, evidence.source)
		if !*apply {
			fmt.Printf("DRY-RUN  %s\n", line)
			byStatus[evidence.status]++
			reconciled++
			continue
		}

		if err := rec.SaveReport(ctx, report); err != nil {
			skipped++
			log.Warn("save final report failed; skipped",
				zap.String("run_id", o.runID), zap.Error(err))
			continue
		}
		fmt.Printf("APPLIED  %s\n", line)
		byStatus[evidence.status]++
		reconciled++
	}

	mode := "DRY-RUN"
	if *apply {
		mode = "APPLIED"
	}
	fmt.Printf("reconcile-orphaned-runs %s: reconciled=%d skipped=%d byStatus=%v\n", mode, reconciled, skipped, byStatus)
	return nil
}

// orphanRun is one RUNNING run_observability row awaiting a terminal verdict.
type orphanRun struct {
	runID, jobID, jobType, attemptID string
	createdAt, startedAt             string
	queueWaitMs                      int64
	reportJSON                       string
}

// jobEvidence is the terminal outcome of a job, resolved either from the
// canonical jobs row ("job") or, when jobs retention has already pruned it,
// from the terminal attempt run of the same job ("attempt").
type jobEvidence struct {
	status     string
	errCode    string
	errMsg     string
	finishedAt time.Time
	durationMs int64
	source     string
}

// resolveJobEvidence resolves the terminal outcome for an orphaned run.
//
// It returns (nil, reason, nil) when nothing terminal can be established: the
// run is then left RUNNING on purpose, because an unsettled job — or a job
// whose only attempt is still running — must not be reported as finished.
func resolveJobEvidence(ctx context.Context, jobDB, obsDB *sql.DB, o orphanRun) (*jobEvidence, string, error) {
	var status, jobErr string
	var completedAt, cancelledAt sql.NullString
	var durationMs int64
	err := jobDB.QueryRowContext(ctx,
		`SELECT status, error, completed_at, cancelled_at, duration_ms FROM jobs WHERE id = ?`, o.jobID).
		Scan(&status, &jobErr, &completedAt, &cancelledAt, &durationMs)

	switch {
	case err == nil:
		if !terminalJobStatus(status) {
			return nil, "job is not terminal", nil
		}
		finished := completedAt.String
		if finished == "" {
			finished = cancelledAt.String
		}
		finishedAt, parseErr := parseRunTime(finished)
		if parseErr != nil {
			return nil, "job has no parsable completed_at", nil
		}
		// FAILED carries the job's error; the historical shape was a literal
		// "error" code with the job's message, preserved here.
		errCode, errMsg := "", ""
		if status == kernobs.StatusFailed {
			errCode, errMsg = "error", jobErr
		}
		return &jobEvidence{status: status, errCode: errCode, errMsg: errMsg, finishedAt: finishedAt, durationMs: durationMs, source: "job"}, "", nil
	case errors.Is(err, sql.ErrNoRows):
		evidence, reason, attemptErr := resolveAttemptEvidence(ctx, obsDB, o.jobID)
		if attemptErr != nil {
			return nil, "", attemptErr
		}
		return evidence, reason, nil
	default:
		return nil, "", fmt.Errorf("lookup job %s: %w", o.jobID, err)
	}
}

// resolveAttemptEvidence reads the terminal attempt run of the same job.
//
// jobs retention prunes terminal job rows; the attempt run records the same
// execution-plane fact (the worker's own start/finish for that job_id) and is
// the only remaining evidence once the job row is gone. The submission rows
// written by SQLiteRunRepository (attempt_id = run_id + ":script") are
// excluded: they are the very rows being repaired, so they can never be their
// own evidence.
func resolveAttemptEvidence(ctx context.Context, obsDB *sql.DB, jobID string) (*jobEvidence, string, error) {
	var status, errCode, errMsg, finished string
	var wallMs int64
	err := obsDB.QueryRowContext(ctx,
		`SELECT status, COALESCE(error_code,''), COALESCE(error,''), COALESCE(finished_at,''), wall_time_ms
		 FROM run_observability
		 WHERE job_id = ? AND attempt_id <> run_id||':script'
		   AND status IN ('SUCCEEDED','FAILED','CANCELLED','ABANDONED')
		 ORDER BY COALESCE(finished_at,'') DESC LIMIT 1`, jobID).
		Scan(&status, &errCode, &errMsg, &finished, &wallMs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "job row missing and no terminal attempt run", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("lookup terminal attempt for job %s: %w", jobID, err)
	}
	finishedAt, parseErr := parseRunTime(finished)
	if parseErr != nil {
		return nil, "terminal attempt has no parsable finished_at", nil
	}
	if status != kernobs.StatusSucceeded && errCode == "" {
		errCode = "ORPHANED_SUBMISSION_RUN"
	}
	return &jobEvidence{status: status, errCode: errCode, errMsg: errMsg, finishedAt: finishedAt, durationMs: wallMs, source: "attempt"}, "", nil
}

func terminalJobStatus(s string) bool {
	switch s {
	case kernobs.StatusSucceeded, kernobs.StatusFailed, kernobs.StatusCancelled:
		return true
	}
	return false
}

func parseRunTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

// buildFinalReport reconstructs the finalized RunReport for an orphaned run.
// Attempt-level runs carry their full report in report_json and only need the
// terminal fields overridden; child script runs were persisted with an empty
// "{}" report, so a minimal report is rebuilt from the row columns.
func buildFinalReport(o orphanRun, evidence *jobEvidence) (*kernobs.RunReport, error) {
	var report kernobs.RunReport
	if o.reportJSON != "" && o.reportJSON != "{}" {
		if err := json.Unmarshal([]byte(o.reportJSON), &report); err != nil {
			return nil, fmt.Errorf("unmarshal report_json: %w", err)
		}
	} else {
		report = kernobs.RunReport{
			RunID:       o.runID,
			JobID:       o.jobID,
			JobType:     o.jobType,
			AttemptID:   o.attemptID,
			QueueWaitMs: o.queueWaitMs,
		}
		if t, err := parseRunTime(o.createdAt); err == nil {
			report.CreatedAt = t
		}
		if t, err := parseRunTime(o.startedAt); err == nil {
			report.StartedAt = t
		}
	}

	report.Status = evidence.status
	report.FinishedAt = evidence.finishedAt
	report.WallTimeMs = evidence.durationMs
	if evidence.status == kernobs.StatusSucceeded {
		report.ErrorCode = ""
		report.Error = ""
	} else {
		report.ErrorCode = evidence.errCode
		report.Error = evidence.errMsg
	}
	return &report, nil
}
