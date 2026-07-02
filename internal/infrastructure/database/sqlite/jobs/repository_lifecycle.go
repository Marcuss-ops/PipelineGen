package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// SetProgress updates progress percentage and emits an event.
func (r *SQLiteStore) SetProgress(ctx context.Context, jobID string, progress int, message string) error {
	query := `UPDATE jobs SET progress = ?, updated_at = ? WHERE id = ?`
	_, err := r.db.ExecContext(ctx, query, progress, timeutil.FormatRFC3339(time.Now()), jobID)
	if err != nil {
		return fmt.Errorf("setProgress: %w", err)
	}
	if message != "" {
		_ = r.AddEvent(ctx, jobID, "progress", message, map[string]any{"progress": progress})
	}
	return nil
}

// ── Complete (atomic transaction) ────────────────────────────────────────

// Complete marks a job as completed with a result. Fenced by lease.
func (r *SQLiteStore) Complete(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, result json.RawMessage) error {
	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)
	resultJSON := string(result)
	if resultJSON == "" || resultJSON == "null" {
		resultJSON = "{}"
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("complete: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Validate ownership
	var status job.Status
	var curWorkerID, curLeaseID string
	var revision int
	err = tx.QueryRowContext(ctx, `SELECT status, worker_id, lease_id, revision FROM jobs WHERE id = ?`, id).
		Scan(&status, &curWorkerID, &curLeaseID, &revision)
	if err != nil {
		if err == sql.ErrNoRows {
			return ErrJobNotFound
		}
		return fmt.Errorf("complete: select: %w", err)
	}
	if err := validateOwnership(id, "complete", status, curWorkerID, curLeaseID, revision,
		workerID, leaseID, int64(expectedRevision), job.StatusRunning, job.StatusFinalizing); err != nil {
		return err
	}

	// Atomic update
	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = 'SUCCEEDED', completed_at = ?, result_json = ?,
		 progress = 100, worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('RUNNING', 'FINALIZING') AND worker_id = ? AND lease_id = ? AND revision = ?`,
		nowStr, resultJSON, nowStr,
		id, workerID, leaseID, expectedRevision)
	if err != nil {
		return fmt.Errorf("complete: update: %w", err)
	}
	if mustRowsAffected(res) == 0 {
		// Race-after-validateOwnership (i.e. validateOwnership passed but a
		// concurrent transaction committed before our UPDATE). The earlier
		// validateOwnership-mismatch case is already counted via the bump
		// inside validateOwnership itself when method="complete". This second
		// bump covers the race window — never a double-count in practice
		// because validateOwnership only bumps on FAILURE (early return),
		// so by the time we reach here, validateOwnership has already passed.
		observability.JobTransitionConflictTotal.WithLabelValues("complete").Inc()
		return ErrTransitionConflict
	}

	// Insert event
	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	_, _ = tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, "job_completed", "job.Job completed successfully", "{}", nowStr)

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("complete: commit: %w", err)
	}
	return nil
}

// ── Fail (atomic transaction) ────────────────────────────────────────────

// Fail marks a job as failed. Fenced by lease.
func (r *SQLiteStore) Fail(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, errMsg string) error {
	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("fail: begin tx: %w", err)
	}
	defer tx.Rollback()

	var status job.Status
	var curWorkerID, curLeaseID string
	var revision int
	err = tx.QueryRowContext(ctx, `SELECT status, worker_id, lease_id, revision FROM jobs WHERE id = ?`, id).
		Scan(&status, &curWorkerID, &curLeaseID, &revision)
	if err != nil {
		if err == sql.ErrNoRows {
			return ErrJobNotFound
		}
		return fmt.Errorf("fail: select: %w", err)
	}
	if err := validateOwnership(id, "fail", status, curWorkerID, curLeaseID, revision,
		workerID, leaseID, int64(expectedRevision), job.StatusRunning, job.StatusFinalizing); err != nil {
		return err
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = 'FAILED', completed_at = ?, error = ?,
		 worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('RUNNING', 'FINALIZING') AND worker_id = ? AND lease_id = ? AND revision = ?`,
		nowStr, errMsg, nowStr,
		id, workerID, leaseID, expectedRevision)
	if err != nil {
		return fmt.Errorf("fail: update: %w", err)
	}
	if mustRowsAffected(res) == 0 {
		// Same race-window rationale as Complete's CAS-fence bump above:
		// validateOwnership would have early-returned on its own mismatch,
		// so we never double-count. See the comment block at Complete's
		// CAS fence for the full invariant.
		observability.JobTransitionConflictTotal.WithLabelValues("fail").Inc()
		return ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	_, _ = tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, "job_failed", errMsg, "{}", nowStr)

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("fail: commit: %w", err)
	}
	return nil
}

// ── ScheduleRetry ────────────────────────────────────────────────────────

// ScheduleRetry transitions a running job to retry_wait (or failed if retries exhausted).
func (r *SQLiteStore) ScheduleRetry(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, backoff time.Duration) error {
	j, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	if j == nil {
		return ErrJobNotFound
	}
	if j.RetryCount >= j.MaxRetries {
		return r.Fail(ctx, id, workerID, leaseID, expectedRevision, "max retries exhausted")
	}

	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("scheduleRetry: begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = 'RETRY_WAIT', error = ?,
		 retry_count = retry_count + 1, worker_id = '', lease_id = '',
		 lease_expiry = NULL, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('RUNNING', 'FINALIZING')
		 AND worker_id = ? AND lease_id = ? AND revision = ?`,
		"scheduled for retry by worker "+workerID, nowStr,
		id, workerID, leaseID, expectedRevision)
	if err != nil {
		return fmt.Errorf("scheduleRetry: update: %w", err)
	}
	if mustRowsAffected(res) == 0 {
		// PR-F: ScheduleRetry does NOT route through validateOwnership
		// (its fenced UPDATE carries the CAS check inline). Bump here on
		// the routed ErrTransitionConflict return. Distinct from the
		// err-typed branch above (which returns a wrapped error, not
		// ErrTransitionConflict) and from Retry's "max retries exhausted"
		// recursion into Fail (which uses method="fail" via the
		// validateOwnership path).
		observability.JobTransitionConflictTotal.WithLabelValues("schedule_retry").Inc()
		return ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	_, _ = tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, "job_retry_wait", "job.Job scheduled for retry", "{}", nowStr)

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("scheduleRetry: commit: %w", err)
	}
	return nil
}

// ── Cancel ───────────────────────────────────────────────────────────────

// Cancel transitions a non-terminal job to cancelled. Idempotent.
func (r *SQLiteStore) Cancel(ctx context.Context, id string) error {
	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'CANCELLED', cancelled_at = ?, worker_id = '',
		 lease_id = '', lease_expiry = NULL, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('QUEUED', 'LEASED', 'RUNNING', 'FINALIZING', 'RETRY_WAIT')`,
		nowStr, nowStr, id)
	if err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		// Idempotent: check if already cancelled/completed/failed.
		j, _ := r.Get(ctx, id)
		if j != nil && j.IsTerminal() {
			return nil
		}
		// PR-F: Cancel does not route through validateOwnership; bump
		// here before returning ErrTransitionConflict. The terminal-state
		// short-circuit above (return nil) is NOT a conflict and
		// intentionally not counted.
		observability.JobTransitionConflictTotal.WithLabelValues("cancel").Inc()
		return ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	_, _ = r.db.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, "job_cancelled", "job.Job cancelled", "{}", nowStr)
	return nil
}

// ── DeadLetter ───────────────────────────────────────────────────────────

func (r *SQLiteStore) DeadLetter(ctx context.Context, id string, errMsg string) error {
	j, err := r.Get(ctx, id)
	if err != nil || j == nil {
		return fmt.Errorf("deadLetter: load job: %w", err)
	}
	payload := string(j.Payload)
	if payload == "" {
		payload = "{}"
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO dead_letter_jobs (job_id, job_type, correlation_id, error, payload_json, retry_count, failed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, j.Type, j.CorrelationID, errMsg, payload, j.RetryCount, timeutil.FormatRFC3339(time.Now()))
	return err
}

// ── Retry (transition retry_wait/failed → queued) ───────────────────────

// Retry re-enqueues a failed or retry_wait job.
func (r *SQLiteStore) Retry(ctx context.Context, id string) (*job.Job, error) {
	j, err := r.Get(ctx, id)
	if err != nil || j == nil {
		return nil, fmt.Errorf("retry: job %s not found", id)
	}
	if j.RetryCount >= j.MaxRetries {
		return nil, fmt.Errorf("retry: exhausted (%d/%d)", j.RetryCount, j.MaxRetries)
	}
	if j.Status != job.StatusRetryWait && j.Status != job.StatusFailed {
		return nil, fmt.Errorf("retry: invalid status %q", j.Status)
	}

	now := timeutil.FormatRFC3339(time.Now())
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'QUEUED', progress = 0, error = '',
		 worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('RETRY_WAIT', 'FAILED') AND revision = ?`,
		now, id, j.Revision)
	if err != nil {
		return nil, fmt.Errorf("retry: %w", err)
	}
	if mustRowsAffected(res) == 0 {
		// PR-F: Retry does not route through validateOwnership; bump
		// here before returning ErrTransitionConflict. Distinct from
		// the inner c.ErrPath branches (retries-exhausted / invalid
		// status) which return pre-wrapped errors.
		observability.JobTransitionConflictTotal.WithLabelValues("retry").Inc()
		return nil, ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", time.Now().UnixNano(), hashutil.RandomString(6))
	_, _ = r.db.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, "job_queued", "job.Job retry activated", "{}", now)

	// PR-Polling / ADR-0002 §D6.5 (June 2026): the requeued job
	// transitions back to QUEUED; wake every sleeping Worker so the
	// retry is picked up immediately. See repository.go::Create for
	// the canonical pattern.
	r.queueChanged()

	return r.Get(ctx, id)
}

// ── Convenience Wrappers ─────────────────────────────────────────────────

// MarkRunningJobsOlderThanFailed moves stale leased/running jobs to failed
// if their lease has expired beyond the given cutoff.
func (r *SQLiteStore) MarkRunningJobsOlderThanFailed(ctx context.Context, cutoff time.Time, reason string) (int, error) {
	now := timeutil.FormatRFC3339(time.Now())
	cutoffStr := timeutil.FormatRFC3339(cutoff)
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'FAILED', completed_at = ?, error = ?,
		 worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE status IN ('LEASED', 'RUNNING', 'FINALIZING') AND lease_expiry < ?`,
		now, reason, now, cutoffStr)
	if err != nil {
		return 0, fmt.Errorf("markRunningJobsOlderThanFailed: %w", err)
	}
	affected, _ := res.RowsAffected()
	return int(affected), nil
}

// AddEvent records a human-readable event on the job timeline.
func (r *SQLiteStore) AddEvent(ctx context.Context, id string, eventType, message string, data map[string]any) error {
	evtID := fmt.Sprintf("evt_%d_%s", time.Now().UnixNano(), hashutil.RandomString(6))
	dataJSON := "{}"
	if data != nil {
		if b, err := json.Marshal(data); err == nil {
			dataJSON = string(b)
		}
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, eventType, message, dataJSON, timeutil.FormatRFC3339(time.Now()))
	if err != nil {
		return fmt.Errorf("addEvent: %w", err)
	}
	return nil
}

// ── Helpers ──────────────────────────────────────────────────────────────

// validateOwnership checks that the current row matches the worker's
// expected lease + revision + status before any fenced UPDATE proceeds.
// PR-F / ADR-0002 §D6.7 (June 2026): the function takes a `method` arg
// so ErrTransitionConflict returns can bump the canonical
// job_transition_conflict_total{method=<name>} counter. The two
// non-TransitionConflict paths (ErrInvalidState, ErrLeaseLost) do NOT
// bump the counter — they're distinct signals
// (worker-called-wrong-transition vs different-worker-on-same-row) and
// merging them under "transition_conflict" would corrupt dashboard
// semantics. The method label is bounded by the 2 callers that route
// through this function (complete / fail); the other 3 fenced-UPDATE
// paths (schedule_retry / cancel / retry) bump at their own CAS-fence
// sites because they DO NOT pass through validateOwnership.
//
// FASE 2b (July 2026): expectedStatus is now variadic — the caller
// passes one or more allowed statuses. Complete/Fail accept both
// RUNNING and FINALIZING.
func validateOwnership(jobID string, method string, currentStatus job.Status,
	currentWorker, currentLease string, currentRevision int,
	expectedWorker, expectedLease string, expectedRevision int64,
	expectedStatuses ...job.Status) error {
	allowed := false
	for _, s := range expectedStatuses {
		if currentStatus == s {
			allowed = true
			break
		}
	}
	if !allowed {
		expectedStrs := make([]string, len(expectedStatuses))
		for i, s := range expectedStatuses {
			expectedStrs[i] = string(s)
		}
		return fmt.Errorf("%w: status %q, expected one of %v", ErrInvalidState, currentStatus, expectedStrs)
	}
	if currentWorker != expectedWorker {
		return fmt.Errorf("%w: worker %q, expected %q", ErrLeaseLost, currentWorker, expectedWorker)
	}
	if currentLease != expectedLease {
		return fmt.Errorf("%w: lease mismatch", ErrLeaseLost)
	}
	if int64(currentRevision) != expectedRevision {
		observability.JobTransitionConflictTotal.WithLabelValues(method).Inc()
		return fmt.Errorf("%w: revision %d, expected %d", ErrTransitionConflict, currentRevision, expectedRevision)
	}
	return nil
}
