package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"velox/go-master/internal/media/models"
	"velox/go-master/pkg/timeutil"
)

// SetProgress updates the progress percentage and emits an event log
// row when a human-readable message is supplied. Progress updates are
// *not* an optimistic-lock operation: progress is monotonically
// non-decreasing and a stale write is harmless (the next progress
// update will overwrite it). Running this through Transition would
// amplify DB contention on long-running jobs for no benefit.
func (r *Repository) SetProgress(ctx context.Context, jobID string, progress int, message string) error {
	query := `UPDATE jobs SET progress = ?, updated_at = ? WHERE id = ?`
	_, err := r.db.ExecContext(ctx, query, progress, timeutil.FormatRFC3339(time.Now()), jobID)
	if err != nil {
		return fmt.Errorf("failed to set progress: %w", err)
	}

	if message != "" {
		_ = r.AddEvent(ctx, jobID, "progress", message, map[string]any{"progress": progress})
	}

	return nil
}

// Complete marks a job as completed and persists the result payload.
// Implemented as a Transition so a concurrent Cancel issued by the
// operator does not silently overwrite our result row.
func (r *Repository) Complete(ctx context.Context, jobID string, result map[string]any) error {
	job, err := r.Get(ctx, jobID)
	if err != nil {
		return fmt.Errorf("complete: load job: %w", err)
	}
	if job == nil {
		return fmt.Errorf("complete: job %s not found", jobID)
	}

	resultJSON, _ := json.Marshal(result)
	if resultJSON == nil {
		resultJSON = []byte("{}")
	}

	now := time.Now()
	_, err = r.Transition(ctx, TransitionRequest{
		JobID:            jobID,
		ExpectedRevision: job.Revision,
		ExpectedStatus:   job.Status,
		NewStatus:        models.StatusCompleted,
		Updates: map[string]any{
			"result_json":  string(resultJSON),
			"progress":     100,
			"completed_at": now,
			"active_key":   "",
		},
	})
	if err != nil {
		return fmt.Errorf("complete: %w", err)
	}

	_ = r.AddEvent(ctx, jobID, "completed", "Job completed successfully", nil)
	return nil
}

// Fail marks a job as failed, recording the error message. Uses
// Transition so a concurrent Retry or Cancel cannot race against
// the failure termination.
func (r *Repository) Fail(ctx context.Context, jobID string, errMsg string) error {
	job, err := r.Get(ctx, jobID)
	if err != nil {
		return fmt.Errorf("fail: load job: %w", err)
	}
	if job == nil {
		return fmt.Errorf("fail: job %s not found", jobID)
	}

	now := time.Now()
	_, err = r.Transition(ctx, TransitionRequest{
		JobID:            jobID,
		ExpectedRevision: job.Revision,
		ExpectedStatus:   job.Status,
		NewStatus:        models.StatusFailed,
		Updates: map[string]any{
			"error":        errMsg,
			"completed_at": now,
			"active_key":   "",
		},
	})
	if err != nil {
		return fmt.Errorf("fail: %w", err)
	}

	_ = r.AddEvent(ctx, jobID, "failed", errMsg, nil)
	return nil
}

// DeadLetter archives a job that has exhausted its retries into the
// dead_letter_jobs table. The main jobs row's status transition is
// performed by the caller (Fail) so the operator-facing row stays
// consistent; this method only writes the parallel DLQ record for
// debugging from the dashboard without grep-ing logs.
//
// Note: dead-lettering is INSERT-only, so it does not need
// optimistic-lock semantics on the parent row.
func (r *Repository) DeadLetter(ctx context.Context, jobID string, errMsg string) error {
	job, err := r.Get(ctx, jobID)
	if err != nil {
		return fmt.Errorf("dead-letter: load job: %w", err)
	}
	if job == nil {
		return fmt.Errorf("dead-letter: job %s not found", jobID)
	}

	var jobType string
	if job.Type != "" {
		jobType = string(job.Type)
	}

	payload := string(job.Payload)
	if payload == "" {
		payload = "{}"
	}

	query := `INSERT INTO dead_letter_jobs
		(job_id, job_type, correlation_id, error, payload_json, retry_count, failed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err = r.db.ExecContext(ctx, query,
		job.ID, jobType, job.CorrelationID, errMsg, payload, job.RetryCount,
		timeutil.FormatRFC3339(time.Now()),
	)
	if err != nil {
		return fmt.Errorf("dead-letter: insert: %w", err)
	}
	return nil
}

// Cancel marks a job as cancelled from any non-terminal state. The
// worker that's currently running this job will see the new status on
// its next IsCancelled probe (see worker.go) and abort cleanly.
func (r *Repository) Cancel(ctx context.Context, jobID string) error {
	job, err := r.Get(ctx, jobID)
	if err != nil {
		return fmt.Errorf("cancel: load job: %w", err)
	}
	if job == nil {
		return fmt.Errorf("cancel: job %s not found", jobID)
	}

	now := time.Now()
	_, err = r.Transition(ctx, TransitionRequest{
		JobID:            jobID,
		ExpectedRevision: job.Revision,
		ExpectedStatus:   job.Status,
		NewStatus:        models.StatusCancelled,
		Updates: map[string]any{
			"cancelled_at": now,
			"active_key":   "",
		},
	})
	if err != nil {
		return fmt.Errorf("cancel: %w", err)
	}

	_ = r.AddEvent(ctx, jobID, "cancelled", "Job cancelled by user", nil)
	return nil
}

// Retry transitions a failed job back to queued for another execution
// cycle. Increments retry_count, clears progress + worker_id, and
// resets the lease token. The Transition expects
// ExpectedStatus='failed'; calling Retry on a non-failed job returns
// the optimistic-lock error.
//
// Round-trip count is 2 (Get + Transition) versus the legacy 1 (a
// single guarded UPDATE). The increased cost is negligible for
// retry, which is a rare path relative to claim + run lifecycle.
//
// Note on `var clearLease`: declared as an explicit typed nil pointer
// so the type switch in Repository.Transition reliably matches the
// `*time.Time` arm and routes through timeutil.FormatPtrRFC3339,
// which serialises nil pointers as SQL NULL. An inline
// `Updates["lease_expiry"] = (*time.Time)(nil)` would be more compact
// but the intent is non-obvious to future maintainers \u2014 using a
// named local variable documents the contract.
func (r *Repository) Retry(ctx context.Context, jobID string) (*models.Job, error) {
	job, err := r.Get(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("retry: load job: %w", err)
	}
	if job == nil {
		return nil, fmt.Errorf("retry: job %s not found", jobID)
	}
	if job.RetryCount >= job.MaxRetries {
		return nil, fmt.Errorf("retry: job %s has exhausted retries (%d/%d)", jobID, job.RetryCount, job.MaxRetries)
	}

	// clearLease is a typed nil pointer; Transition routes it through
	// the *time.Time arm of the type switch and emits SQL NULL.
	var clearLease *time.Time
	claimed, err := r.Transition(ctx, TransitionRequest{
		JobID:            jobID,
		ExpectedRevision: job.Revision,
		ExpectedStatus:   models.StatusFailed,
		NewStatus:        models.StatusQueued,
		Updates: map[string]any{
			"retry_count":  job.RetryCount + 1,
			"error":        "",
			"progress":     0,
			"worker_id":    "",
			"lease_expiry": clearLease,
			// active_key is preserved so concurrent duplicate retries
			// can still converge through enqueue idempotency.
		},
	})
	if err != nil {
		return nil, fmt.Errorf("retry: %w", err)
	}
	return claimed, nil
}
