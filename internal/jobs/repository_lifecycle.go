package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	timeutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
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
	var status Status
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
	if err := validateOwnership(id, status, curWorkerID, curLeaseID, revision,
		workerID, leaseID, int64(expectedRevision), StatusRunning); err != nil {
		return err
	}

	// Atomic update
	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = 'completed', completed_at = ?, result_json = ?,
		 progress = 100, worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status = 'running' AND worker_id = ? AND lease_id = ? AND revision = ?`,
		nowStr, resultJSON, nowStr,
		id, workerID, leaseID, expectedRevision)
	if err != nil {
		return fmt.Errorf("complete: update: %w", err)
	}
	if mustRowsAffected(res) == 0 {
		return ErrTransitionConflict
	}

	// Insert event
	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	_, _ = tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, "job_completed", "Job completed successfully", "{}", nowStr)

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

	var status Status
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
	if err := validateOwnership(id, status, curWorkerID, curLeaseID, revision,
		workerID, leaseID, int64(expectedRevision), StatusRunning); err != nil {
		return err
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = 'failed', completed_at = ?, error = ?,
		 worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status = 'running' AND worker_id = ? AND lease_id = ? AND revision = ?`,
		nowStr, errMsg, nowStr,
		id, workerID, leaseID, expectedRevision)
	if err != nil {
		return fmt.Errorf("fail: update: %w", err)
	}
	if mustRowsAffected(res) == 0 {
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
		`UPDATE jobs SET status = 'retry_wait', error = ?,
		 retry_count = retry_count + 1, worker_id = '', lease_id = '',
		 lease_expiry = NULL, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status = 'running'
		 AND worker_id = ? AND lease_id = ? AND revision = ?`,
		"scheduled for retry by worker "+workerID, nowStr,
		id, workerID, leaseID, expectedRevision)
	if err != nil {
		return fmt.Errorf("scheduleRetry: update: %w", err)
	}
	if mustRowsAffected(res) == 0 {
		return ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	_, _ = tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, "job_retry_wait", "Job scheduled for retry", "{}", nowStr)

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
		`UPDATE jobs SET status = 'cancelled', cancelled_at = ?, worker_id = '',
		 lease_id = '', lease_expiry = NULL, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('queued', 'leased', 'running', 'retry_wait')`,
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
		return ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	_, _ = r.db.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, "job_cancelled", "Job cancelled", "{}", nowStr)
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
func (r *SQLiteStore) Retry(ctx context.Context, id string) (*Job, error) {
	j, err := r.Get(ctx, id)
	if err != nil || j == nil {
		return nil, fmt.Errorf("retry: job %s not found", id)
	}
	if j.RetryCount >= j.MaxRetries {
		return nil, fmt.Errorf("retry: exhausted (%d/%d)", j.RetryCount, j.MaxRetries)
	}
	if j.Status != StatusRetryWait && j.Status != StatusFailed {
		return nil, fmt.Errorf("retry: invalid status %q", j.Status)
	}

	now := timeutil.FormatRFC3339(time.Now())
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'queued', progress = 0, error = '',
		 worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('retry_wait', 'failed') AND revision = ?`,
		now, id, j.Revision)
	if err != nil {
		return nil, fmt.Errorf("retry: %w", err)
	}
	if mustRowsAffected(res) == 0 {
		return nil, ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", time.Now().UnixNano(), hashutil.RandomString(6))
	_, _ = r.db.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, "job_queued", "Job retry activated", "{}", now)
	return r.Get(ctx, id)
}

// ── Convenience Wrappers ─────────────────────────────────────────────────

// RequeueExpiredLeasesNoArg is a convenience wrapper that passes time.Now()
// and a default limit of 1000.
func (r *SQLiteStore) RequeueExpiredLeasesNoArg(ctx context.Context) error {
	_, err := r.RequeueExpiredLeases(ctx, time.Now(), 1000)
	return err
}

// MarkRunningJobsOlderThanFailed moves stale leased/running jobs to failed
// if their lease has expired beyond the given cutoff.
func (r *SQLiteStore) MarkRunningJobsOlderThanFailed(ctx context.Context, cutoff time.Time, reason string) (int, error) {
	now := timeutil.FormatRFC3339(time.Now())
	cutoffStr := timeutil.FormatRFC3339(cutoff)
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'failed', completed_at = ?, error = ?,
		 worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE status IN ('leased', 'running') AND lease_expiry < ?`,
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

func validateOwnership(jobID string, currentStatus Status,
	currentWorker, currentLease string, currentRevision int,
	expectedWorker, expectedLease string, expectedRevision int64,
	expectedStatus Status) error {
	if currentStatus != expectedStatus {
		return fmt.Errorf("%w: status %q, expected %q", ErrInvalidState, currentStatus, expectedStatus)
	}
	if currentWorker != expectedWorker {
		return fmt.Errorf("%w: worker %q, expected %q", ErrLeaseLost, currentWorker, expectedWorker)
	}
	if currentLease != expectedLease {
		return fmt.Errorf("%w: lease mismatch", ErrLeaseLost)
	}
	if int64(currentRevision) != expectedRevision {
		return fmt.Errorf("%w: revision %d, expected %d", ErrTransitionConflict, currentRevision, expectedRevision)
	}
	return nil
}
