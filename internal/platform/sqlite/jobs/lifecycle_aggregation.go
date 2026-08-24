package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── ScheduleRetry ────────────────────────────────────────────────────────

// ScheduleRetry transitions a running job to retry_wait (or failed if retries exhausted).
func (r *SQLiteStore) ScheduleRetry(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, errMsg string, backoff time.Duration) error {
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
		errMsg, nowStr,
		id, workerID, leaseID, expectedRevision)
	if err != nil {
		return fmt.Errorf("scheduleRetry: update: %w", err)
	}
	if mustRowsAffected(res) == 0 {
		// PR-F: ScheduleRetry does NOT route through validateOwnership
		// (its fenced UPDATE carries the CAS check inline). Bump here on
		// the routed job.ErrTransitionConflict return. Distinct from the
		// err-typed branch above (which returns a wrapped error, not
		// job.ErrTransitionConflict) and from Retry's "max retries exhausted"
		// recursion into Fail (which uses method="fail" via the
		// validateOwnership path).
		observability.JobTransitionConflictTotal.WithLabelValues("schedule_retry").Inc()
		return job.ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	evtData, _ := json.Marshal(map[string]string{"error": errMsg})
	if _, err := tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, "job_retry_wait", "job.Job scheduled for retry", string(evtData), nowStr); err != nil {
		return fmt.Errorf("scheduleRetry: insert job event: %w", err)
	}

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
		// here before returning job.ErrTransitionConflict. The terminal-state
		// short-circuit above (return nil) is NOT a conflict and
		// intentionally not counted.
		observability.JobTransitionConflictTotal.WithLabelValues("cancel").Inc()
		return job.ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), hashutil.RandomString(6))
	if _, err := r.db.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, "job_cancelled", "job.Job cancelled", "{}", nowStr); err != nil {
		return fmt.Errorf("cancel: insert job event: %w", err)
	}
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
		// here before returning job.ErrTransitionConflict. Distinct from
		// the inner c.ErrPath branches (retries-exhausted / invalid
		// status) which return pre-wrapped errors.
		observability.JobTransitionConflictTotal.WithLabelValues("retry").Inc()
		return nil, job.ErrTransitionConflict
	}

	evtID := fmt.Sprintf("evt_%d_%s", time.Now().UnixNano(), hashutil.RandomString(6))
	if _, err := r.db.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, id, "job_queued", "job.Job retry activated", "{}", now); err != nil {
		return nil, fmt.Errorf("retry: insert job event: %w", err)
	}

	// PR-Polling / ADR-0002 §D6.5 (June 2026): the requeued job
	// transitions back to QUEUED; wake every sleeping Worker so the
	// retry is picked up immediately. See repository.go::Create for
	// the canonical pattern.
	r.queueChanged()

	return r.Get(ctx, id)
}
