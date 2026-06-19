package deliveries

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
)

// ClaimNext atomically pulls the next eligible delivery for processing.
// Joins delivery_destinations (enabled) and artifacts (READY) to filter out
// rows whose target is disabled or whose artifact isn't ready. Sets the
// row to LEASED with a worker-scoped lock and stored lease expiry.
//
// Returns nil, nil when no matching row exists (not an error).
func (r *SQLiteRepository) ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration) (*Delivery, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("deliveries: begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)
	leaseExpires := now.Add(leaseTTL)
	leaseExpiresStr := timeutil.FormatRFC3339(leaseExpires)
	lockedBy := fmt.Sprintf("dlv-%s-%d", workerID, now.UnixNano())

	var d Delivery
	var nextAttempt, lockedUntil, completedAt sql.NullString
	var createdAt, updatedAt string

	err = tx.QueryRowContext(ctx, `
		SELECT d.id, d.artifact_id, d.destination_id, d.provider, d.status,
			d.attempt_count, d.max_attempts, d.next_attempt_at,
			d.locked_by, d.locked_until, d.remote_id, d.remote_url,
			d.last_error_code, d.last_error_message,
			d.created_at, d.updated_at, d.completed_at
		FROM deliveries d
		JOIN delivery_destinations dd ON dd.destination_id = d.destination_id
		JOIN artifacts a ON a.id = d.artifact_id
		WHERE d.status IN ('PENDING', 'RETRY_WAIT')
			AND (d.next_attempt_at IS NULL OR d.next_attempt_at <= ?)
			AND (d.locked_until IS NULL OR d.locked_until < ?)
			AND dd.enabled = 1
			AND a.status = 'READY'
		ORDER BY d.created_at ASC
		LIMIT 1
	`, nowStr, nowStr).Scan(
		&d.ID, &d.ArtifactID, &d.DestinationID, &d.Provider, &d.Status,
		&d.AttemptCount, &d.MaxAttempts, &nextAttempt,
		&d.LockedBy, &lockedUntil, &d.RemoteID, &d.RemoteURL,
		&d.LastErrorCode, &d.LastErrorMessage,
		&createdAt, &updatedAt, &completedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("deliveries: claim: %w", err)
	}

	// Atomically mark as LEASED
	result, err := tx.ExecContext(ctx, `
		UPDATE deliveries
		SET status = 'LEASED', locked_by = ?, locked_until = ?, updated_at = ?
		WHERE id = ? AND status IN ('PENDING', 'RETRY_WAIT')
	`, lockedBy, leaseExpiresStr, nowStr, d.ID)
	if err != nil {
		return nil, fmt.Errorf("deliveries: claim update: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return nil, nil // another worker claimed it
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("deliveries: commit claim: %w", err)
	}

	d.Status = StatusLeased
	d.LockedBy = lockedBy
	d.LockedUntil = &leaseExpires
	d.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	d.UpdatedAt = now
	if nextAttempt.Valid {
		t, _ := time.Parse(time.RFC3339, nextAttempt.String)
		d.NextAttemptAt = &t
	}
	return &d, nil
}

// RenewLease extends the lock window for an actively-running delivery.
// Only succeeds if the row is still RUNNING and still owned by the same
// worker (locked_by matches). This prevents zombie workers from
// artificially keeping dead leases alive.
func (r *SQLiteRepository) RenewLease(ctx context.Context, id, lockedBy string, leaseTTL time.Duration) error {
	expiresAt := timeutil.FormatRFC3339(time.Now().UTC().Add(leaseTTL))
	now := timeutil.FormatRFC3339(time.Now().UTC())

	_, err := r.db.ExecContext(ctx, `
		UPDATE deliveries SET locked_until = ?, updated_at = ?
		WHERE id = ? AND locked_by = ? AND status = 'RUNNING'
	`, expiresAt, now, id, lockedBy)
	if err != nil {
		return fmt.Errorf("deliveries: renew lease %s: %w", id, err)
	}
	return nil
}
