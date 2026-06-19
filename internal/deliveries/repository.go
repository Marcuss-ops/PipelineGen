package deliveries

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// SQLiteRepository persists delivery records in the unified database.
type SQLiteRepository struct {
	db *sql.DB
}

// NewSQLiteRepository creates a new delivery repository.
func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	return &SQLiteRepository{db: db}
}

// SchemaDDL returns the DDL for the deliveries and delivery_destinations tables.
func SchemaDDL() string {
	return `
CREATE TABLE IF NOT EXISTS delivery_destinations (
    destination_id TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    provider       TEXT NOT NULL DEFAULT 'drive',
    enabled        INTEGER NOT NULL DEFAULT 1,
    config_json    TEXT NOT NULL DEFAULT '{}',
    created_at     TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS deliveries (
    id                TEXT PRIMARY KEY,
    artifact_id       TEXT NOT NULL,
    destination_id    TEXT NOT NULL DEFAULT '',
    provider          TEXT NOT NULL DEFAULT 'drive',
    status            TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING','LEASED','RUNNING','RETRY_WAIT','SUCCEEDED','FAILED','BLOCKED_AUTH','CANCELLED')),
    attempt_count     INTEGER NOT NULL DEFAULT 0,
    max_attempts      INTEGER NOT NULL DEFAULT 5,
    next_attempt_at   TEXT,
    locked_by         TEXT NOT NULL DEFAULT '',
    locked_until      TEXT,
    remote_id         TEXT NOT NULL DEFAULT '',
    remote_url        TEXT NOT NULL DEFAULT '',
    last_error_code   TEXT NOT NULL DEFAULT '',
    last_error_message TEXT NOT NULL DEFAULT '',
    idempotency_key   TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at        TEXT NOT NULL DEFAULT (datetime('now')),
    completed_at      TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_deliveries_idempotency
    ON deliveries(idempotency_key) WHERE idempotency_key <> '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_deliveries_artifact_dest
    ON deliveries(artifact_id, destination_id);

CREATE INDEX IF NOT EXISTS idx_deliveries_status ON deliveries(status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_deliveries_artifact ON deliveries(artifact_id);
CREATE INDEX IF NOT EXISTS idx_deliveries_locked ON deliveries(locked_by, locked_until);
`
}

// ── Create ─────────────────────────────────────────────────────────────

func (r *SQLiteRepository) Create(ctx context.Context, d *Delivery) error {
	now := time.Now().UTC()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	d.UpdatedAt = now

	if d.IdempotencyKey == "" {
		d.IdempotencyKey = computeIdempotencyKey(d.ArtifactID, d.DestinationID, d.Provider)
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO deliveries (id, artifact_id, destination_id, provider, status,
			attempt_count, max_attempts, next_attempt_at, locked_by,
			remote_id, remote_url, last_error_code, last_error_message,
			idempotency_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, d.ID, d.ArtifactID, d.DestinationID, d.Provider, d.Status,
		d.AttemptCount, d.MaxAttempts, timeutil.FormatPtrRFC3339(d.NextAttemptAt),
		d.LockedBy, d.RemoteID, d.RemoteURL, d.LastErrorCode, d.LastErrorMessage,
		d.IdempotencyKey, timeutil.FormatRFC3339(d.CreatedAt), timeutil.FormatRFC3339(d.UpdatedAt))
	if err != nil {
		return fmt.Errorf("deliveries: create %s: %w", d.ID, err)
	}
	return nil
}

// ── ClaimNext (fixed: JOIN destinations + artifacts, PENDING+RETRY_WAIT) ───

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

// ── Get ────────────────────────────────────────────────────────────────

func (r *SQLiteRepository) Get(ctx context.Context, id string) (*Delivery, error) {
	var d Delivery
	var nextAttempt, lockedUntil, completedAt sql.NullString
	var createdAt, updatedAt string

	err := r.db.QueryRowContext(ctx, `SELECT `+deliveryColumns+` FROM deliveries WHERE id = ?`, id).Scan(
		&d.ID, &d.ArtifactID, &d.DestinationID, &d.Provider, &d.Status,
		&d.AttemptCount, &d.MaxAttempts, &nextAttempt,
		&d.LockedBy, &lockedUntil, &d.RemoteID, &d.RemoteURL,
		&d.LastErrorCode, &d.LastErrorMessage,
		&d.IdempotencyKey, &d.StorageKey, &d.SHA256, &d.SizeBytes, &d.MimeType,
		&createdAt, &updatedAt, &completedAt,
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
	if lockedUntil.Valid {
		t, _ := time.Parse(time.RFC3339, lockedUntil.String)
		d.LockedUntil = &t
	}
	return &d, nil
}

// ── RenewLease ─────────────────────────────────────────────────────────

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

// ── Atomic CompleteDelivery ────────────────────────────────────────────

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

// ── Atomic FailDelivery ────────────────────────────────────────────────

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

// ── Atomic RetryDelivery ───────────────────────────────────────────────

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

// ── Atomic BlockDeliveryAuth ───────────────────────────────────────────

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

// ── RequeueStale ───────────────────────────────────────────────────────

func (r *SQLiteRepository) RequeueStale(ctx context.Context, now time.Time, limit int) ([]Delivery, error) {
	nowStr := timeutil.FormatRFC3339(now.UTC())

	rows, err := r.db.QueryContext(ctx, `SELECT `+deliveryColumns+` FROM deliveries
		WHERE status IN ('LEASED', 'RUNNING')
			AND locked_until IS NOT NULL AND locked_until <= ?
		LIMIT ?`, nowStr, limit)
	if err != nil {
		return nil, fmt.Errorf("deliveries: requeue query: %w", err)
	}
	defer rows.Close()

	var stale []Delivery
	for rows.Next() {
		var d Delivery
		var nextAttempt, lockedUntil, completedAt sql.NullString
		var createdAt, updatedAt string
		if err := rows.Scan(
			&d.ID, &d.ArtifactID, &d.DestinationID, &d.Provider, &d.Status,
			&d.AttemptCount, &d.MaxAttempts, &nextAttempt,
			&d.LockedBy, &lockedUntil, &d.RemoteID, &d.RemoteURL,
			&d.LastErrorCode, &d.LastErrorMessage, &d.IdempotencyKey,
			&d.StorageKey, &d.SHA256, &d.SizeBytes, &d.MimeType,
			&createdAt, &updatedAt, &completedAt,
		); err != nil {
			return nil, fmt.Errorf("deliveries: requeue scan: %w", err)
		}
		d.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		d.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		stale = append(stale, d)
	}

	// Return stale deliveries to PENDING atomically in a transaction.
	if len(stale) > 0 {
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("deliveries: requeue begin: %w", err)
		}
		defer tx.Rollback()

		for _, d := range stale {
			_, _ = tx.ExecContext(ctx, `
				UPDATE deliveries
				SET status = 'PENDING', locked_by = '', locked_until = NULL, updated_at = ?
				WHERE id = ?
			`, nowStr, d.ID)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("deliveries: commit requeue: %w", err)
		}
	}

	return stale, rows.Err()
}

// ── ListByArtifact ─────────────────────────────────────────────────────

func (r *SQLiteRepository) ListByArtifact(ctx context.Context, artifactID string) ([]Delivery, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+deliveryColumns+` FROM deliveries WHERE artifact_id = ? ORDER BY created_at`, artifactID)
	if err != nil {
		return nil, fmt.Errorf("deliveries: list by artifact: %w", err)
	}
	defer rows.Close()

	var deliveries []Delivery
	for rows.Next() {
		var d Delivery
		var nextAttempt, lockedUntil, completedAt sql.NullString
		var createdAt, updatedAt string
		if err := rows.Scan(
			&d.ID, &d.ArtifactID, &d.DestinationID, &d.Provider, &d.Status,
			&d.AttemptCount, &d.MaxAttempts, &nextAttempt,
			&d.LockedBy, &lockedUntil, &d.RemoteID, &d.RemoteURL,
			&d.LastErrorCode, &d.LastErrorMessage, &d.IdempotencyKey,
			&d.StorageKey, &d.SHA256, &d.SizeBytes, &d.MimeType,
			&createdAt, &updatedAt, &completedAt,
		); err != nil {
			return nil, fmt.Errorf("deliveries: scan: %w", err)
		}
		d.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		d.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		deliveries = append(deliveries, d)
	}
	return deliveries, rows.Err()
}

// ── Idempotency support ────────────────────────────────────────────────

func (r *SQLiteRepository) FindByIdempotencyKey(ctx context.Context, key string) (*Delivery, error) {
	var d Delivery
	var nextAttempt, lockedUntil, completedAt sql.NullString
	var createdAt, updatedAt string

	err := r.db.QueryRowContext(ctx, `SELECT `+deliveryColumns+` FROM deliveries WHERE idempotency_key = ? AND idempotency_key <> ''`, key).Scan(
		&d.ID, &d.ArtifactID, &d.DestinationID, &d.Provider, &d.Status,
		&d.AttemptCount, &d.MaxAttempts, &nextAttempt,
		&d.LockedBy, &lockedUntil, &d.RemoteID, &d.RemoteURL,
		&d.LastErrorCode, &d.LastErrorMessage, &d.IdempotencyKey,
		&d.StorageKey, &d.SHA256, &d.SizeBytes, &d.MimeType,
		&createdAt, &updatedAt, &completedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("deliveries: find by idempotency: %w", err)
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	d.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &d, nil
}

func (r *SQLiteRepository) UpsertDestination(ctx context.Context, dest *DeliveryDestination) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO delivery_destinations (destination_id, name, provider, enabled, config_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(destination_id) DO UPDATE SET
			name = excluded.name, provider = excluded.provider,
			enabled = excluded.enabled, config_json = excluded.config_json
	`, dest.DestinationID, dest.Name, dest.Provider, dest.Enabled, dest.ConfigJSON, dest.CreatedAt)
	if err != nil {
		return fmt.Errorf("deliveries: upsert dest: %w", err)
	}
	return nil
}

func (r *SQLiteRepository) GetDestination(ctx context.Context, destinationID string) (*DeliveryDestination, error) {
	var dest DeliveryDestination
	err := r.db.QueryRowContext(ctx, `SELECT destination_id, name, provider, enabled, config_json, created_at FROM delivery_destinations WHERE destination_id = ?`, destinationID).
		Scan(&dest.DestinationID, &dest.Name, &dest.Provider, &dest.Enabled, &dest.ConfigJSON, &dest.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("deliveries: get dest: %w", err)
	}
	return &dest, nil
}

// ── Helpers ────────────────────────────────────────────────────────────

// deliveryColumns is the canonical SELECT column list for deliveries.
// deliveryColumns is the canonical SELECT column list. No table alias prefix —
// callers append "FROM deliveries" (or "FROM deliveries d" for JOIN queries).
const deliveryColumns = `id, artifact_id, destination_id, provider, status, attempt_count, max_attempts, next_attempt_at, locked_by, locked_until, remote_id, remote_url, last_error_code, last_error_message, idempotency_key, storage_key, sha256, size_bytes, mime_type, created_at, updated_at, completed_at`

func computeIdempotencyKey(artifactID, destinationID, provider string) string {
	raw := artifactID + destinationID + provider
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// Compile-time checks
var _ Repository = (*SQLiteRepository)(nil)
