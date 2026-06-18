package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/pkg/hashutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// SetProgress updates progress percentage and emits an event.
// Not an optimistic-lock operation — progress is monotonically non-decreasing.
func (r *Repository) SetProgress(ctx context.Context, jobID string, progress int, message string) error {
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

// Complete marks a job as SUCCEEDED inside an atomic transaction with
// worker+fencing validation. Caller MUST pass WorkerID + LeaseID + Revision.
func (r *Repository) Complete(ctx context.Context, cmd CompleteJob) (*models.Job, error) {
	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)
	resultJSON := string(cmd.ResultJSON)
	if resultJSON == "" {
		resultJSON = "{}"
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("complete: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Validate ownership
	var status models.JobStatus
	var workerID, leaseID string
	var revision int
	err = tx.QueryRowContext(ctx, `SELECT status, worker_id, lease_id, revision FROM jobs WHERE id = ?`, cmd.JobID).
		Scan(&status, &workerID, &leaseID, &revision)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrJobNotFound
		}
		return nil, fmt.Errorf("complete: select: %w", err)
	}
	if err := validateOwnership(cmd.JobID, status, workerID, leaseID, revision,
		cmd.WorkerID, cmd.LeaseID, int64(cmd.Revision), models.StatusRunning); err != nil {
		return nil, err
	}

	// Atomic update
	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = ?, completed_at = ?, result_json = ?,
		 progress = 100, worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status = ? AND worker_id = ? AND lease_id = ? AND revision = ?`,
		models.StatusSucceeded, nowStr, resultJSON, nowStr,
		cmd.JobID, models.StatusRunning, cmd.WorkerID, cmd.LeaseID, cmd.Revision)
	if err != nil {
		return nil, fmt.Errorf("complete: update: %w", err)
	}
	if mustRowsAffected(res) == 0 {
		return nil, ErrTransitionConflict
	}

	// Insert event
	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	_, _ = tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, cmd.JobID, "job_succeeded", "Job completed successfully", "{}", nowStr)

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("complete: commit: %w", err)
	}
	return r.Get(ctx, cmd.JobID)
}

// ── Fail (atomic transaction) ────────────────────────────────────────────

// Fail marks a job as FAILED inside an atomic transaction with ownership validation.
func (r *Repository) Fail(ctx context.Context, cmd FailJob) (*models.Job, error) {
	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("fail: begin tx: %w", err)
	}
	defer tx.Rollback()

	var status models.JobStatus
	var workerID, leaseID string
	var revision int
	err = tx.QueryRowContext(ctx, `SELECT status, worker_id, lease_id, revision FROM jobs WHERE id = ?`, cmd.JobID).
		Scan(&status, &workerID, &leaseID, &revision)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrJobNotFound
		}
		return nil, fmt.Errorf("fail: select: %w", err)
	}
	if err := validateOwnership(cmd.JobID, status, workerID, leaseID, revision,
		cmd.WorkerID, cmd.LeaseID, int64(cmd.Revision), models.StatusRunning); err != nil {
		return nil, err
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = ?, completed_at = ?, error = ?,
		 worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status = ? AND worker_id = ? AND lease_id = ? AND revision = ?`,
		models.StatusFailed, nowStr, cmd.Error, nowStr,
		cmd.JobID, models.StatusRunning, cmd.WorkerID, cmd.LeaseID, cmd.Revision)
	if err != nil {
		return nil, fmt.Errorf("fail: update: %w", err)
	}
	if mustRowsAffected(res) == 0 {
		return nil, ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	_, _ = tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, cmd.JobID, "job_failed", cmd.Error, "{}", nowStr)

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("fail: commit: %w", err)
	}
	return r.Get(ctx, cmd.JobID)
}

// ── ScheduleRetry ────────────────────────────────────────────────────────

// ScheduleRetry transitions a RUNNING job to RETRY_WAIT (or FAILED if retries exhausted).
func (r *Repository) ScheduleRetry(ctx context.Context, cmd ScheduleRetry) (*models.Job, error) {
	job, err := r.Get(ctx, cmd.JobID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, ErrJobNotFound
	}
	if job.RetryCount >= job.MaxRetries {
		return r.Fail(ctx, FailJob{JobID: cmd.JobID, WorkerID: cmd.WorkerID, LeaseID: cmd.LeaseID, Revision: int64(job.Revision), Error: "max retries exhausted"})
	}

	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("scheduleRetry: begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = 'RETRY_WAIT', error = ?,
		 retry_count = retry_count + 1, worker_id = '', lease_id = '',
		 lease_expiry = NULL, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status = 'RUNNING'
		 AND worker_id = ? AND lease_id = ? AND revision = ?`,
		"scheduled for retry by worker "+cmd.WorkerID, nowStr,
		cmd.JobID, cmd.WorkerID, cmd.LeaseID, cmd.Revision)
	if err != nil {
		return nil, fmt.Errorf("scheduleRetry: update: %w", err)
	}
	if mustRowsAffected(res) == 0 {
		return nil, ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	_, _ = tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, cmd.JobID, "job_retry_wait", "Job scheduled for retry", "{}", nowStr)

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("scheduleRetry: commit: %w", err)
	}
	return r.Get(ctx, cmd.JobID)
}

// ── RequestCancel ────────────────────────────────────────────────────────

// RequestCancel transitions a non-terminal job to CANCELLED. Idempotent.
func (r *Repository) RequestCancel(ctx context.Context, cmd RequestCancel) (*models.Job, error) {
	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, cancelled_at = ?, worker_id = '',
		 lease_id = '', lease_expiry = NULL, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('PENDING', 'LEASED', 'RUNNING', 'RETRY_WAIT')`,
		models.StatusCancelled, nowStr, nowStr, cmd.JobID)
	if err != nil {
		return nil, fmt.Errorf("requestCancel: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		job, _ := r.Get(ctx, cmd.JobID)
		if job != nil && job.Status.IsTerminal() {
			return job, nil // idempotent
		}
		return nil, ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	_, _ = r.db.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, cmd.JobID, "job_cancelled", "Job cancelled", "{}", nowStr)
	return r.Get(ctx, cmd.JobID)
}

// ── ConfirmCancelled ─────────────────────────────────────────────────────

// ConfirmCancelled is called after a worker acknowledges a cancellation.
func (r *Repository) ConfirmCancelled(ctx context.Context, cmd ConfirmCancelled) (*models.Job, error) {
	now := timeutil.FormatRFC3339(time.Now())
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET cancelled_at = ?, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status = ? AND worker_id = ? AND lease_id = ? AND revision = ?`,
		now, now, cmd.JobID, models.StatusCancelled, cmd.WorkerID, cmd.LeaseID, cmd.Revision)
	if err != nil {
		return nil, fmt.Errorf("confirmCancelled: %w", err)
	}
	if mustRowsAffected(res) == 0 {
		return nil, ErrTransitionConflict
	}
	return r.Get(ctx, cmd.JobID)
}

// ── DeadLetter ───────────────────────────────────────────────────────────

func (r *Repository) DeadLetter(ctx context.Context, jobID string, errMsg string) error {
	job, err := r.Get(ctx, jobID)
	if err != nil || job == nil {
		return fmt.Errorf("deadLetter: load job: %w", err)
	}
	payload := string(job.Payload)
	if payload == "" {
		payload = "{}"
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO dead_letter_jobs (job_id, job_type, correlation_id, error, payload_json, retry_count, failed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		job.ID, string(job.Type), job.CorrelationID, errMsg, payload, job.RetryCount, timeutil.FormatRFC3339(time.Now()))
	return err
}

// ── Retry (transition RETRY_WAIT → PENDING via periodic scheduler) ──────

func (r *Repository) Retry(ctx context.Context, jobID string) (*models.Job, error) {
	job, err := r.Get(ctx, jobID)
	if err != nil || job == nil {
		return nil, fmt.Errorf("retry: job %s not found", jobID)
	}
	if job.RetryCount >= job.MaxRetries {
		return nil, fmt.Errorf("retry: exhausted (%d/%d)", job.RetryCount, job.MaxRetries)
	}
	if job.Status != models.StatusRetryWait && job.Status != models.StatusFailed {
		return nil, fmt.Errorf("retry: invalid status %q", job.Status)
	}

	now := timeutil.FormatRFC3339(time.Now())
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'PENDING', progress = 0, error = '',
		 worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('RETRY_WAIT', 'FAILED') AND revision = ?`,
		now, jobID, job.Revision)
	if err != nil {
		return nil, fmt.Errorf("retry: %w", err)
	}
	if mustRowsAffected(res) == 0 {
		return nil, ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", time.Now().UnixNano(), hashutil.RandomString(6))
	_, _ = r.db.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, jobID, "job_pending", "Job retry activated", "{}", now)
	return r.Get(ctx, jobID)
}

// ── Helpers ──────────────────────────────────────────────────────────────

func validateOwnership(jobID string, currentStatus models.JobStatus,
	currentWorker, currentLease string, currentRevision int,
	expectedWorker, expectedLease string, expectedRevision int64,
	expectedStatus models.JobStatus) error {
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
