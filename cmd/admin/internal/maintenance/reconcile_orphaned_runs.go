package maintenance

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"database/sql"
	"encoding/json"
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
// For each RUNNING run it looks up the canonical job (primary DB) and, if
// that job is terminal, finalizes the run through the canonical
// SQLiteRecorder.SaveReport path (so run_observability + job_attempts +
// report_json are updated atomically). The run inherits the job's status
// (NOT ABANDONED — a job that succeeded must not be reported as
// worker-lost). wall_time_ms ← jobs.duration_ms, finished_at ←
// jobs.completed_at, error ← jobs.error (FAILED only).
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

	rows, err := dbSet.Observability.DB.QueryContext(ctx,
		`SELECT run_id, job_id, job_type, attempt_id, created_at, started_at, queue_wait_ms, report_json
		 FROM run_observability
		 WHERE status = 'RUNNING'
		 ORDER BY started_at ASC`)
	if err != nil {
		return fmt.Errorf("select running runs: %w", err)
	}
	defer rows.Close()

	type orphan struct {
		runID, jobID, jobType, attemptID string
		createdAt, startedAt             string
		queueWaitMs                      int64
		reportJSON                       string
	}
	var orphans []orphan
	for rows.Next() {
		var o orphan
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
		var jobStatus, jobErr, completedAtStr, cancelledAtStr string
		var durationMs int64
		var completedAt, cancelledAt sql.NullString
		err := dbSet.Primary.DB.QueryRowContext(ctx,
			`SELECT status, error, completed_at, cancelled_at, duration_ms FROM jobs WHERE id = ?`, o.jobID).
			Scan(&jobStatus, &jobErr, &completedAt, &cancelledAt, &durationMs)
		if err == sql.ErrNoRows {
			skipped++
			log.Warn("orphaned run has no job; skipped", zap.String("run_id", o.runID), zap.String("job_id", o.jobID))
			continue
		}
		if err != nil {
			return fmt.Errorf("lookup job %s: %w", o.jobID, err)
		}
		completedAtStr, cancelledAtStr = completedAt.String, cancelledAt.String

		if !terminalJobStatus(jobStatus) {
			skipped++
			log.Warn("orphaned run's job is not terminal; skipped",
				zap.String("run_id", o.runID), zap.String("job_id", o.jobID), zap.String("job_status", jobStatus))
			continue
		}

		finished := completedAtStr
		if finished == "" {
			finished = cancelledAtStr
		}
		finishedAt, err := parseRunTime(finished)
		if err != nil {
			skipped++
			log.Warn("orphaned run's job has unparsable completed_at; skipped",
				zap.String("run_id", o.runID), zap.String("job_id", o.jobID), zap.String("completed_at", finished))
			continue
		}

		report, err := buildFinalReport(o, jobStatus, jobErr, finishedAt, durationMs)
		if err != nil {
			skipped++
			log.Warn("build final report failed; skipped",
				zap.String("run_id", o.runID), zap.Error(err))
			continue
		}

		if !*apply {
			fmt.Printf("DRY-RUN  %-48s job=%-12s -> %s (wall=%dms)\n", o.runID, o.jobID, jobStatus, durationMs)
			byStatus[jobStatus]++
			reconciled++
			continue
		}

		if err := rec.SaveReport(ctx, report); err != nil {
			skipped++
			log.Warn("save final report failed; skipped",
				zap.String("run_id", o.runID), zap.Error(err))
			continue
		}
		fmt.Printf("APPLIED  %-48s job=%-12s -> %s (wall=%dms)\n", o.runID, o.jobID, jobStatus, durationMs)
		byStatus[jobStatus]++
		reconciled++
	}

	mode := "DRY-RUN"
	if *apply {
		mode = "APPLIED"
	}
	fmt.Printf("reconcile-orphaned-runs %s: reconciled=%d skipped=%d byStatus=%v\n", mode, reconciled, skipped, byStatus)
	return nil
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
func buildFinalReport(o struct {
	runID, jobID, jobType, attemptID string
	createdAt, startedAt             string
	queueWaitMs                      int64
	reportJSON                       string
}, jobStatus, jobErr string, finishedAt time.Time, durationMs int64) (*kernobs.RunReport, error) {
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

	report.Status = jobStatus
	report.FinishedAt = finishedAt
	report.WallTimeMs = durationMs
	if jobStatus == kernobs.StatusFailed {
		report.ErrorCode = "error"
		report.Error = jobErr
	} else {
		report.ErrorCode = ""
		report.Error = ""
	}
	return &report, nil
}
