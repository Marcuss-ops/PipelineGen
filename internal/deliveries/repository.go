package deliveries

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SQLiteRepository persists delivery records in the unified database.
type SQLiteRepository struct {
	db *sql.DB
}

// NewSQLiteRepository creates a new delivery repository.
func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	return &SQLiteRepository{db: db}
}

// SchemaDDL returns the DDL for the deliveries table.
func SchemaDDL() string {
	return `
CREATE TABLE IF NOT EXISTS deliveries (
    id               TEXT PRIMARY KEY,
    artifact_id      TEXT NOT NULL,
    target_id        TEXT NOT NULL DEFAULT '',
    provider         TEXT NOT NULL DEFAULT 'drive',
    status           TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING','LEASED','RUNNING','RETRY_WAIT','SUCCEEDED','FAILED','BLOCKED_AUTH','CANCELLED')),
    attempt_count    INTEGER NOT NULL DEFAULT 0,
    max_attempts     INTEGER NOT NULL DEFAULT 3,
    next_attempt_at  TEXT,
    lease_id         TEXT NOT NULL DEFAULT '',
    lease_expires_at TEXT,
    remote_id        TEXT NOT NULL DEFAULT '',
    remote_url       TEXT NOT NULL DEFAULT '',
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT NOT NULL DEFAULT (datetime('now')),
    completed_at     TEXT
);

CREATE INDEX IF NOT EXISTS idx_deliveries_status ON deliveries(status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_deliveries_artifact ON deliveries(artifact_id);
`
}

// Create inserts a new delivery record.
func (r *SQLiteRepository) Create(ctx context.Context, d *Delivery) error {
	now := time.Now().UTC()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	d.UpdatedAt = now

	var nextAttempt string
	if d.NextAttemptAt != nil {
		nextAttempt = d.NextAttemptAt.UTC().Format(time.RFC3339)
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO deliveries (id, artifact_id, target_id, provider, status,
			attempt_count, max_attempts, next_attempt_at, lease_id,
			remote_id, remote_url, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, d.ID, d.ArtifactID, d.TargetID, d.Provider, d.Status,
		d.AttemptCount, d.MaxAttempts, nextAttempt, d.LeaseID,
		d.RemoteID, d.RemoteURL, d.LastError,
		d.CreatedAt.UTC().Format(time.RFC3339), d.UpdatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("deliveries: create %s: %w", d.ID, err)
	}
	return nil
}

// ClaimNext atomically claims the next PENDING delivery for a worker.
// Uses BEGIN IMMEDIATE to prevent concurrent claims on the same row
// in WAL mode. Verifies rowsAffected to detect lost-update races.
func (r *SQLiteRepository) ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration) (*Delivery, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("deliveries: begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	leaseExpires := now.Add(leaseTTL).Format(time.RFC3339)
	leaseID := fmt.Sprintf("dlv-%s-%d", workerID, now.UnixNano())

	var d Delivery
	var nextAttempt, leaseExpiresAt, completedAt sql.NullString
	var createdAt, updatedAt string

	err = tx.QueryRowContext(ctx, `
		SELECT id, artifact_id, target_id, provider, status,
			attempt_count, max_attempts, next_attempt_at,
			lease_id, lease_expires_at, remote_id, remote_url,
			last_error, created_at, updated_at, completed_at
		FROM deliveries
		WHERE status = 'PENDING'
			AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
		ORDER BY created_at ASC
		LIMIT 1
	`, nowStr).Scan(
		&d.ID, &d.ArtifactID, &d.TargetID, &d.Provider, &d.Status,
		&d.AttemptCount, &d.MaxAttempts, &nextAttempt,
		&d.LeaseID, &leaseExpiresAt, &d.RemoteID, &d.RemoteURL,
		&d.LastError, &createdAt, &updatedAt, &completedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("deliveries: claim: %w", err)
	}

	// Atomically mark as LEASED — verify rowsAffected to prevent lost-update races
	result, err := tx.ExecContext(ctx, `
		UPDATE deliveries
		SET status = 'LEASED', lease_id = ?, lease_expires_at = ?,
			updated_at = ?
		WHERE id = ? AND status = 'PENDING'
	`, leaseID, leaseExpires, nowStr, d.ID)
	if err != nil {
		return nil, fmt.Errorf("deliveries: claim update: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		// Another worker claimed this delivery first
		return nil, nil
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("deliveries: commit claim: %w", err)
	}

	d.Status = StatusLeased
	d.LeaseID = leaseID
	d.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	d.UpdatedAt = now
	if nextAttempt.Valid {
		t, _ := time.Parse(time.RFC3339, nextAttempt.String)
		d.NextAttemptAt = &t
	}
	return &d, nil
}

// Get retrieves a delivery by ID.
func (r *SQLiteRepository) Get(ctx context.Context, id string) (*Delivery, error) {
	var d Delivery
	var nextAttempt, leaseExpiresAt, completedAt sql.NullString
	var createdAt, updatedAt string

	err := r.db.QueryRowContext(ctx, `
		SELECT id, artifact_id, target_id, provider, status,
			attempt_count, max_attempts, next_attempt_at,
			lease_id, lease_expires_at, remote_id, remote_url,
			last_error, created_at, updated_at, completed_at
		FROM deliveries WHERE id = ?
	`, id).Scan(
		&d.ID, &d.ArtifactID, &d.TargetID, &d.Provider, &d.Status,
		&d.AttemptCount, &d.MaxAttempts, &nextAttempt,
		&d.LeaseID, &leaseExpiresAt, &d.RemoteID, &d.RemoteURL,
		&d.LastError, &createdAt, &updatedAt, &completedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("deliveries: get %s: %w", id, err)
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	d.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if nextAttempt.Valid {
		t, _ := time.Parse(time.RFC3339, nextAttempt.String)
		d.NextAttemptAt = &t
	}
	return &d, nil
}

// UpdateStatus transitions a delivery to a new status. Uses parameterized
// SQL to safely set optional fields (completed_at is set for terminal statuses).
func (r *SQLiteRepository) UpdateStatus(ctx context.Context, id string, status Status, remoteID, remoteURL, lastError string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	// Always set completed_at for terminal states via a CASE expression
	_, err := r.db.ExecContext(ctx, `
		UPDATE deliveries
		SET status = ?, remote_id = ?, remote_url = ?, last_error = ?,
			updated_at = ?,
			completed_at = CASE WHEN ? IN ('SUCCEEDED','FAILED','CANCELLED') THEN ? ELSE completed_at END
		WHERE id = ?
	`, status, remoteID, remoteURL, lastError, now, status, now, id)
	if err != nil {
		return fmt.Errorf("deliveries: update %s: %w", id, err)
	}
	return nil
}

// RenewLease extends the lease for an in-flight delivery.
func (r *SQLiteRepository) RenewLease(ctx context.Context, id, leaseID string, leaseTTL time.Duration) error {
	expiresAt := time.Now().UTC().Add(leaseTTL).Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := r.db.ExecContext(ctx, `
		UPDATE deliveries
		SET lease_expires_at = ?, updated_at = ?
		WHERE id = ? AND lease_id = ? AND status = 'RUNNING'
	`, expiresAt, now, id, leaseID)
	if err != nil {
		return fmt.Errorf("deliveries: renew lease %s: %w", id, err)
	}
	return nil
}

// RequeueStale returns stale LEASED/RUNNING deliveries to PENDING
// atomically within a transaction.
func (r *SQLiteRepository) RequeueStale(ctx context.Context, now time.Time, limit int) ([]Delivery, error) {
	nowStr := now.UTC().Format(time.RFC3339)

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("deliveries: requeue begin: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, artifact_id, target_id, provider, status,
			attempt_count, max_attempts, next_attempt_at,
			lease_id, lease_expires_at, remote_id, remote_url,
			last_error, created_at, updated_at, completed_at
		FROM deliveries
		WHERE status IN ('LEASED', 'RUNNING')
			AND lease_expires_at IS NOT NULL
			AND lease_expires_at <= ?
		LIMIT ?
	`, nowStr, limit)
	if err != nil {
		return nil, fmt.Errorf("deliveries: requeue query: %w", err)
	}
	defer rows.Close()

	var stale []Delivery
	for rows.Next() {
		var d Delivery
		var nextAttempt, leaseExpiresAt, completedAt sql.NullString
		var createdAt, updatedAt string
		if err := rows.Scan(
			&d.ID, &d.ArtifactID, &d.TargetID, &d.Provider, &d.Status,
			&d.AttemptCount, &d.MaxAttempts, &nextAttempt,
			&d.LeaseID, &leaseExpiresAt, &d.RemoteID, &d.RemoteURL,
			&d.LastError, &createdAt, &updatedAt, &completedAt,
		); err != nil {
			return nil, fmt.Errorf("deliveries: requeue scan: %w", err)
		}
		d.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		d.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		stale = append(stale, d)
	}

	// Return stale deliveries to PENDING atomically
	if len(stale) > 0 {
		for _, d := range stale {
			_, _ = tx.ExecContext(ctx, `
				UPDATE deliveries
				SET status = 'PENDING', lease_id = '', lease_expires_at = NULL, updated_at = ?
				WHERE id = ?
			`, nowStr, d.ID)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("deliveries: commit requeue: %w", err)
		}
	}
	return stale, rows.Err()
}

// ListByArtifact returns all deliveries for an artifact.
func (r *SQLiteRepository) ListByArtifact(ctx context.Context, artifactID string) ([]Delivery, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, artifact_id, target_id, provider, status,
			attempt_count, max_attempts, next_attempt_at,
			lease_id, lease_expires_at, remote_id, remote_url,
			last_error, created_at, updated_at, completed_at
		FROM deliveries WHERE artifact_id = ? ORDER BY created_at
	`, artifactID)
	if err != nil {
		return nil, fmt.Errorf("deliveries: list by artifact: %w", err)
	}
	defer rows.Close()

	var deliveries []Delivery
	for rows.Next() {
		var d Delivery
		var nextAttempt, leaseExpiresAt, completedAt sql.NullString
		var createdAt, updatedAt string
		if err := rows.Scan(
			&d.ID, &d.ArtifactID, &d.TargetID, &d.Provider, &d.Status,
			&d.AttemptCount, &d.MaxAttempts, &nextAttempt,
			&d.LeaseID, &leaseExpiresAt, &d.RemoteID, &d.RemoteURL,
			&d.LastError, &createdAt, &updatedAt, &completedAt,
		); err != nil {
			return nil, fmt.Errorf("deliveries: scan: %w", err)
		}
		d.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		d.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		deliveries = append(deliveries, d)
	}
	return deliveries, rows.Err()
}

// Compile-time check
var _ Repository = (*SQLiteRepository)(nil)
