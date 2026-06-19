package deliveries

import (
	"context"
	"fmt"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
)

// CompleteDelivery transitions a delivery from RUNNING/LEASED to SUCCEEDED.
// Only succeeds if the row is still owned by the same worker (locked_by
// matches). Records the remote_id/url and event-trail entry atomically.
func (r *SQLiteRepository) CompleteDelivery(ctx context.Context, cmd CompleteDeliveryCommand) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("deliveries: complete begin: %w", err)
	}
	defer tx.Rollback()

	now := timeutil.FormatRFC3339(time.Now().UTC())
	res, err := tx.ExecContext(ctx, `
		UPDATE deliveries
		SET status = 'SUCCEEDED', remote_id = ?, remote_url = ?,
			completed_at = ?, updated_at = ?, locked_by = '', locked_until = NULL
		WHERE id = ? AND status IN ('RUNNING', 'LEASED') AND locked_by = ?
	`, cmd.RemoteID, cmd.RemoteURL, now, now, cmd.DeliveryID, cmd.LockedBy)
	if err != nil {
		return fmt.Errorf("deliveries: complete update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("deliveries: complete %s: delivery not found or locked by different worker", cmd.DeliveryID)
	}

	// Event
	evtID := fmt.Sprintf("evt_%d", time.Now().UnixNano())
	_, _ = tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, cmd.DeliveryID, "delivery_succeeded", "Delivery completed", "{}", now)

	return tx.Commit()
}

// FailDelivery transitions a delivery from RUNNING/LEASED to FAILED.
// Only succeeds if the row is still owned by the same worker. Records
// the error code/message and event-trail entry atomically.
func (r *SQLiteRepository) FailDelivery(ctx context.Context, cmd FailDeliveryCommand) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("deliveries: fail begin: %w", err)
	}
	defer tx.Rollback()

	now := timeutil.FormatRFC3339(time.Now().UTC())
	res, err := tx.ExecContext(ctx, `
		UPDATE deliveries
		SET status = 'FAILED', last_error_code = ?, last_error_message = ?,
			completed_at = ?, updated_at = ?, locked_by = '', locked_until = NULL
		WHERE id = ? AND status IN ('RUNNING', 'LEASED') AND locked_by = ?
	`, cmd.ErrorCode, cmd.ErrorMessage, now, now, cmd.DeliveryID, cmd.LockedBy)
	if err != nil {
		return fmt.Errorf("deliveries: fail update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("deliveries: fail %s: delivery not found or locked by different worker", cmd.DeliveryID)
	}

	evtID := fmt.Sprintf("evt_%d", time.Now().UnixNano())
	_, _ = tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, cmd.DeliveryID, "delivery_failed", cmd.ErrorMessage, "{}", now)

	return tx.Commit()
}

// RetryDelivery transitions a delivery from RUNNING/LEASED to RETRY_WAIT
// with an incremented attempt_count and a scheduled next_attempt_at.
// Only succeeds if the row is still owned by the same worker.
func (r *SQLiteRepository) RetryDelivery(ctx context.Context, cmd RetryDeliveryCommand) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("deliveries: retry begin: %w", err)
	}
	defer tx.Rollback()

	now := timeutil.FormatRFC3339(time.Now().UTC())
	nextAttempt := timeutil.FormatRFC3339(cmd.NextAttemptAt)
	res, err := tx.ExecContext(ctx, `
		UPDATE deliveries
		SET status = 'RETRY_WAIT', attempt_count = attempt_count + 1,
			next_attempt_at = ?, last_error_code = ?, last_error_message = ?,
			updated_at = ?, locked_by = '', locked_until = NULL
		WHERE id = ? AND status IN ('RUNNING', 'LEASED') AND locked_by = ?
	`, nextAttempt, cmd.ErrorCode, cmd.ErrorMessage, now, cmd.DeliveryID, cmd.LockedBy)
	if err != nil {
		return fmt.Errorf("deliveries: retry update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("deliveries: retry %s: delivery not found or locked by different worker", cmd.DeliveryID)
	}

	evtID := fmt.Sprintf("evt_%d", time.Now().UnixNano())
	_, _ = tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, cmd.DeliveryID, "delivery_retry_wait", cmd.ErrorMessage, "{}", now)

	return tx.Commit()
}

// BlockDeliveryAuth transitions a delivery from RUNNING/LEASED to BLOCKED_AUTH.
// Indicates the remote destination rejected credentials — typically an OAuth
// refresh is needed before retrying. Only succeeds if the row is still
// owned by the same worker.
func (r *SQLiteRepository) BlockDeliveryAuth(ctx context.Context, cmd BlockAuthCommand) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("deliveries: block auth begin: %w", err)
	}
	defer tx.Rollback()

	now := timeutil.FormatRFC3339(time.Now().UTC())
	res, err := tx.ExecContext(ctx, `
		UPDATE deliveries
		SET status = 'BLOCKED_AUTH', last_error_code = ?, last_error_message = ?,
			completed_at = ?, updated_at = ?, locked_by = '', locked_until = NULL
		WHERE id = ? AND status IN ('RUNNING', 'LEASED') AND locked_by = ?
	`, cmd.ErrorCode, cmd.ErrorMessage, now, now, cmd.DeliveryID, cmd.LockedBy)
	if err != nil {
		return fmt.Errorf("deliveries: block auth update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("deliveries: block auth %s: delivery not found or locked by different worker", cmd.DeliveryID)
	}

	evtID := fmt.Sprintf("evt_%d", time.Now().UnixNano())
	_, _ = tx.ExecContext(ctx, `INSERT INTO job_events (id, job_id, type, message, data_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, cmd.DeliveryID, "delivery_blocked_auth", cmd.ErrorMessage, "{}", now)

	return tx.Commit()
}
