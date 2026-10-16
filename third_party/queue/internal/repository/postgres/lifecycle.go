// lifecycle.go owns the mid-flight transitions of a claimed job: fail
// (requeue vs permanent), lease renew, explicit retry, producer cancel and
// progress reporting. Each transition keeps the append-only attempt/event
// history consistent with the row state.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
)

// Fail marks a running job failed. Jobs that have not exhausted their attempts
// are requeued; otherwise they are permanently failed.
func (r *Repository) Fail(ctx context.Context, id, workerID, reason string) error {
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var attempts, maxAttempts int
	err = tx.QueryRowContext(ctx, `
		SELECT attempt_count, max_attempts
		FROM render_jobs
		WHERE id = $1 AND state = `+stateLiteral(model.StateRunning)+` AND current_worker_id = $2
		FOR UPDATE`, id, workerID).Scan(&attempts, &maxAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("job %s is not running or not owned by %s", id, workerID)
	}
	if err != nil {
		return err
	}

	permanent := model.ShouldFailPermanently(attempts, maxAttempts)

	attempt, err := runningAttemptID(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := finishAttempt(ctx, tx, attempt, attemptStatusFailed, "", reason); err != nil {
		return err
	}

	var update string
	if permanent {
		update = `
			UPDATE render_jobs
			SET state = ` + stateLiteral(model.StateFailed) + `, failed_at = now(), error_message = $2,
			    current_worker_id = NULL, lease_until = NULL
			WHERE id = $1`
	} else {
		update = `
			UPDATE render_jobs
			SET state = ` + stateLiteral(model.StatePending) + `, error_message = $2, queued_at = now(),
			    current_worker_id = NULL, lease_until = NULL
			WHERE id = $1`
	}
	if _, err := tx.ExecContext(ctx, update, id, reason); err != nil {
		return err
	}

	eventType := eventJobRequeued
	if permanent {
		eventType = eventJobFailed
	}
	if err := recordEvent(ctx, tx, eventType, id, attempt, workerID, map[string]any{"reason": reason}); err != nil {
		return err
	}
	return tx.Commit()
}

// FailPermanently marks a running job terminally failed regardless of its
// normal retry budget. It is reserved for non-retryable failures such as a
// missing required publisher configuration.
func (r *Repository) FailPermanently(ctx context.Context, id, workerID, reason string) error {
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var attemptCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT attempt_count FROM render_jobs
		WHERE id = $1 AND state = `+stateLiteral(model.StateRunning)+` AND current_worker_id = $2
		FOR UPDATE`, id, workerID).Scan(&attemptCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("job %s is not running or not owned by %s", id, workerID)
		}
		return err
	}
	attempt, err := runningAttemptID(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := finishAttempt(ctx, tx, attempt, attemptStatusFailed, "", reason); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET state = `+stateLiteral(model.StateFailed)+`, failed_at = now(), error_message = $2,
		    current_worker_id = NULL, lease_until = NULL
		WHERE id = $1`, id, reason); err != nil {
		return err
	}
	if err := recordEvent(ctx, tx, eventJobFailed, id, attempt, workerID, map[string]any{"reason": reason, "permanent": true}); err != nil {
		return err
	}
	return tx.Commit()
}

// Renew extends the lease for a running job owned by workerID.
func (r *Repository) Renew(ctx context.Context, id, workerID string) error {
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET lease_until = $3
		WHERE id = $1 AND state IN `+stateIn(model.StateRunning, model.StateFinalizing)+` AND current_worker_id = $2`,
		id, workerID, time.Now().Add(r.lease))
	if err != nil {
		return err
	}
	n, err := rowsAffected(res)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("job %s is not running or not owned by %s", id, workerID)
	}

	attempt, err := runningAttemptID(ctx, tx, id)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE render_attempts
		SET last_renewed_at = now()
		WHERE id = $1`, attempt); err != nil {
		return err
	}
	if err := recordEvent(ctx, tx, eventLeaseRenewed, id, attempt, workerID, nil); err != nil {
		return err
	}
	return tx.Commit()
}

// Retry resets a failed job back to pending state for re-execution.
func (r *Repository) Retry(ctx context.Context, id string) error {
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var state string
	err = tx.QueryRowContext(ctx, `SELECT state FROM render_jobs WHERE id = $1 FOR UPDATE`, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("job %s: %w", id, repository.ErrNotFound)
	}
	if err != nil {
		return err
	}
	if state != string(model.StateFailed) {
		return fmt.Errorf("job %s is in state %q, cannot retry non-failed job", id, state)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET state = `+stateLiteral(model.StatePending)+`, error_message = NULL, queued_at = now(),
		    current_worker_id = NULL, lease_until = NULL,
		    progress_frames_done = 0, progress_total_frames = 0,
		    progress_last_frame_at = NULL, progress_worker = ''
		WHERE id = $1`, id)
	if err != nil {
		return err
	}

	if err := recordEvent(ctx, tx, eventJobRequeued, id, "", "", map[string]any{"action": "retry"}); err != nil {
		return err
	}
	return tx.Commit()
}

// Cancel moves a job that has not reached a terminal state to cancelled.
// Cancelled is terminal: ClaimState only selects pending/rendered rows and
// RequeueExpired only touches running/finalizing rows, so a cancelled job is
// never claimed again and never requeued after its lease elapses — a
// cancelled job can never trigger a new Chronon render. An in-flight attempt
// (the job was already claimed when the cancel landed) is closed as
// 'cancelled' in render_attempts so the history explains why the attempt
// ended without a re-render. Attempts are deliberately NOT incremented: every
// claim that would have invoked Chronon already happened.
func (r *Repository) Cancel(ctx context.Context, id string) error {
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var state string
	err = tx.QueryRowContext(ctx, `SELECT state FROM render_jobs WHERE id = $1 FOR UPDATE`, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("job %s: %w", id, repository.ErrNotFound)
	}
	if err != nil {
		return err
	}
	if state == string(model.StateCancelled) {
		// Idempotent: the producer already cancelled this job.
		return tx.Commit()
	}
	if model.IsTerminalState(model.State(state)) {
		return fmt.Errorf("job %s is in state %q, cannot cancel terminal job", id, state)
	}

	// Close any in-flight attempt (running/finalizing rows) as cancelled so
	// the append-only attempt history explains the terminal state.
	attempt, err := runningAttemptID(ctx, tx, id)
	if err != nil {
		return err
	}
	if attempt != "" {
		if err := finishAttempt(ctx, tx, attempt, attemptStatusCancelled, "cancelled", "cancelled by producer"); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET state = `+stateLiteral(model.StateCancelled)+`, current_worker_id = NULL, lease_until = NULL,
		    error_message = NULL,
		    progress_frames_done = 0, progress_total_frames = 0,
		    progress_last_frame_at = NULL, progress_worker = ''
		WHERE id = $1`, id); err != nil {
		return err
	}

	if err := recordEvent(ctx, tx, eventJobCancelled, id, attempt, "", map[string]any{"from": state}); err != nil {
		return err
	}
	return tx.Commit()
}

// SetProgress stores the latest render progress from the lease-owning worker.
// The conditional UPDATE (state = StateRunning AND current_worker_id=workerID)
// makes a stale worker's report a no-op instead of a corruption vector: the
// report either lands on the current owner's row or affects zero rows.
func (r *Repository) SetProgress(ctx context.Context, id, workerID string, p model.Progress) error {
	if id == "" || workerID == "" {
		return fmt.Errorf("job id and worker id are required")
	}
	if p.FramesDone < 0 || p.TotalFrames < 0 {
		return fmt.Errorf("job %s: negative progress values are invalid", id)
	}
	if p.TotalFrames > 0 && p.FramesDone > p.TotalFrames {
		return fmt.Errorf("job %s: frames_done %d exceeds frames_total %d", id, p.FramesDone, p.TotalFrames)
	}
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	res, err := r.db.ExecContext(ctx, `
		UPDATE render_jobs
		SET progress_frames_done = $2,
		    progress_total_frames = $3,
		    progress_last_frame_at = now(),
		    progress_worker = $4
		WHERE id = $1 AND state = `+stateLiteral(model.StateRunning)+` AND current_worker_id = $4`,
		id, p.FramesDone, p.TotalFrames, workerID)
	if err != nil {
		return err
	}
	n, err := rowsAffected(res)
	if err != nil {
		return err
	}
	if n == 0 {
		// Distinguish "unknown job" from "not the lease owner" for callers.
		var state string
		err := r.db.QueryRowContext(ctx, `SELECT state FROM render_jobs WHERE id = $1`, id).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("job %s: %w", id, repository.ErrNotFound)
		}
		if err != nil {
			return err
		}
		if state != string(model.StateRunning) {
			return fmt.Errorf("job %s is in state %q, cannot report progress for non-running job", id, state)
		}
		return fmt.Errorf("job %s is owned by another worker", id)
	}
	return nil
}
