// Package outbox implements the transactional outbox pattern for idempotent
// Qdrant indexing. The outbox guarantees that embedding jobs are:
//
//   - At-least-once delivered (workers claim and retry on failure)
//   - Idempotent (composite unique key prevents duplicate processing)
//   - Observable (pending/in_flight/processed/failed/dead_letter states)
//
// The outbox is written in the SAME transaction as media_assets upserts,
// ensuring atomicity even across multiple PipelineGen instances.
//
// DEPRECATED — Legacy media_index_outbox (Blocco 4, June 2026):
//
// The media_index_outbox table is the original outbox pattern designed for
// local-indexing workers that poll a shared SQLite table. With the move
// toward truly stateless workers (Point 4 of the architecture roadmap),
// this local-SQLite-polling outbox will be replaced by:
//
//   1. Job-system-based indexing (jobs.Repository + worker pool) — for
//      embedding/indexing work that is naturally async and retryable.
//   2. Direct Qdrant upserts — for hot-path catalog sync where the worker
//      already has the embedding payload in memory.
//
// Migration plan:
//   Phase 1 (now):    Keep media_index_outbox running.  Catalog sync and
//                     artlist enrichment still depend on it.
//   Phase 2 (Point 4): Route all indexing through the job system.
//                      media_index_outbox becomes a write-only dead table.
//   Phase 3:           Remove the outbox worker, the table, and this
//                      package.  No new code should depend on outbox.
//
// New integrations MUST use the job system (internal/jobs) for async work
// or call vectorstore.Service directly for synchronous upserts.  Do NOT
// add new consumers of this package.
package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// OutboxEntry represents a single row in media_index_outbox.
type OutboxEntry struct {
	ID                int64     `json:"id"`
	AssetID           string    `json:"asset_id"`
	ContentHash       string    `json:"content_hash"`
	EmbeddingModel    string    `json:"embedding_model"`
	EmbeddingVersion  string    `json:"embedding_version"`
	CollectionVersion string    `json:"collection_version"`
	Status            string    `json:"status"`
	PayloadJSON       string    `json:"payload_json"`
	AttemptCount      int       `json:"attempt_count"`
	LastError         string    `json:"last_error"`
	NextAttemptAt     time.Time `json:"next_attempt_at"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// Repository provides CRUD operations on the media_index_outbox table.
// All mutations are idempotent thanks to the composite unique constraint.
type Repository struct {
	db  *sql.DB
	log *zap.Logger
}

// NewRepository creates a new outbox repository.
func NewRepository(db *sql.DB, log *zap.Logger) *Repository {
	return &Repository{db: db, log: log}
}

// DB returns the underlying database connection (for transactional callers).
func (r *Repository) DB() *sql.DB {
	return r.db
}

// Payload is the denormalized data a worker needs to process an indexing job.
type Payload struct {
	AssetID           string `json:"asset_id"`
	Name              string `json:"name,omitempty"`
	LocalPath         string `json:"local_path,omitempty"`
	EmbeddingModel    string `json:"embedding_model"`
	EmbeddingVersion  string `json:"embedding_version"`
	CollectionVersion string `json:"collection_version"`
}

// Enqueue inserts an outbox entry. If an identical (asset_id, content_hash,
// embedding_model, embedding_version, collection_version) tuple already exists,
// the insert is silently ignored (idempotent).
//
// Must be called inside an existing transaction for atomicity with the
// media_assets upsert.
func (r *Repository) Enqueue(ctx context.Context, tx *sql.Tx, entry *OutboxEntry) error {
	payload, err := json.Marshal(Payload{
		AssetID:           entry.AssetID,
		EmbeddingModel:    entry.EmbeddingModel,
		EmbeddingVersion:  entry.EmbeddingVersion,
		CollectionVersion: entry.CollectionVersion,
	})
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO media_index_outbox
			(asset_id, content_hash, embedding_model, embedding_version, collection_version, payload_json, next_attempt_at)
		VALUES (?, ?, ?, ?, ?, ?, datetime('now'))
	`, entry.AssetID, entry.ContentHash, entry.EmbeddingModel, entry.EmbeddingVersion, entry.CollectionVersion, string(payload))
	if err != nil {
		return fmt.Errorf("enqueue outbox entry for %s: %w", entry.AssetID, err)
	}
	return nil
}

// Claim picks the next pending outbox entry, atomically transitions it to
// in_flight, and returns it. Returns nil (no error) when no work is available.
// The claim uses a 5-minute in_flight timeout: if a worker crashes, the entry
// will eventually revert to pending via ReclaimStale.
func (r *Repository) Claim(ctx context.Context) (*OutboxEntry, error) {
	row := r.db.QueryRowContext(ctx, `
		UPDATE media_index_outbox
		SET status = 'in_flight',
		    attempt_count = attempt_count + 1,
		    updated_at = datetime('now')
		WHERE id = (
			SELECT id FROM media_index_outbox
			WHERE status = 'pending'
			  AND next_attempt_at <= datetime('now')
			ORDER BY next_attempt_at ASC, created_at ASC
			LIMIT 1
		)
		RETURNING id, asset_id, content_hash, embedding_model, embedding_version,
		          collection_version, status, payload_json, attempt_count,
		          last_error, next_attempt_at, created_at, updated_at
	`)

	entry := &OutboxEntry{}
	var nextAttempt, createdAt, updatedAt string
	err := row.Scan(
		&entry.ID, &entry.AssetID, &entry.ContentHash, &entry.EmbeddingModel,
		&entry.EmbeddingVersion, &entry.CollectionVersion, &entry.Status,
		&entry.PayloadJSON, &entry.AttemptCount, &entry.LastError,
		&nextAttempt, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil // no work
	}
	if err != nil {
		return nil, fmt.Errorf("claim outbox entry: %w", err)
	}

	entry.NextAttemptAt, _ = time.Parse("2006-01-02 15:04:05", nextAttempt)
	entry.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	entry.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)

	return entry, nil
}

// ClaimBatch atomically claims up to `limit` pending entries, transitioning
// them to in_flight. Returns the claimed entries; the slice is empty when
// no work is available. Each entry is independently failed / completed by
// the worker that pulls it off the channel.
//
// Mirrors Claim's transition semantics but in one round-trip; the worker
// pool dispatches each returned entry to a concurrent goroutine, so the
// PR-2 batch+workers path doesn't have to reassemble a row-per-claim
// sequence with N+1 round-trips per batch.
func (r *Repository) ClaimBatch(ctx context.Context, limit int) ([]*OutboxEntry, error) {
	if r.db == nil {
		return nil, fmt.Errorf("outbox.Repository: db is nil")
	}
	if limit <= 0 {
		return []*OutboxEntry{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		UPDATE media_index_outbox
		SET status = 'in_flight',
		    attempt_count = attempt_count + 1,
		    updated_at = datetime('now')
		WHERE id IN (
			SELECT id FROM media_index_outbox
			WHERE status = 'pending'
			  AND next_attempt_at <= datetime('now')
			ORDER BY next_attempt_at ASC, created_at ASC
			LIMIT ?
		)
		RETURNING id, asset_id, content_hash, embedding_model, embedding_version,
		          collection_version, status, payload_json, attempt_count,
		          last_error, next_attempt_at, created_at, updated_at
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("claim batch outbox: %w", err)
	}
	defer rows.Close()

	out := make([]*OutboxEntry, 0, limit)
	for rows.Next() {
		entry := &OutboxEntry{}
		var nextAttempt, createdAt, updatedAt string
		if err := rows.Scan(
			&entry.ID, &entry.AssetID, &entry.ContentHash, &entry.EmbeddingModel,
			&entry.EmbeddingVersion, &entry.CollectionVersion, &entry.Status,
			&entry.PayloadJSON, &entry.AttemptCount, &entry.LastError,
			&nextAttempt, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan claimed outbox entry: %w", err)
		}
		entry.NextAttemptAt, _ = time.Parse("2006-01-02 15:04:05", nextAttempt)
		entry.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		entry.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("claim batch outbox rows: %w", err)
	}
	return out, nil
}

// Complete marks an outbox entry as successfully processed.
func (r *Repository) Complete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE media_index_outbox
		SET status = 'processed', updated_at = datetime('now')
		WHERE id = ?
	`, id)
	if err != nil {
		return fmt.Errorf("complete outbox entry %d: %w", id, err)
	}
	return nil
}

// Fail marks an outbox entry as failed. If the attempt count exceeds
// maxAttempts, the entry is moved to dead_letter instead. Otherwise it is
// scheduled for retry with exponential backoff.
func (r *Repository) Fail(ctx context.Context, id int64, lastError string, maxAttempts int) error {
	var attemptCount int
	err := r.db.QueryRowContext(ctx, "SELECT attempt_count FROM media_index_outbox WHERE id = ?", id).Scan(&attemptCount)
	if err != nil {
		return fmt.Errorf("get attempt count for outbox %d: %w", id, err)
	}

	if attemptCount >= maxAttempts {
		_, err = r.db.ExecContext(ctx, `
			UPDATE media_index_outbox
			SET status = 'dead_letter', last_error = ?, updated_at = datetime('now')
			WHERE id = ?
		`, lastError, id)
		if err != nil {
			return fmt.Errorf("dead-letter outbox entry %d: %w", id, err)
		}
		r.log.Warn("outbox entry dead-lettered",
			zap.Int64("id", id),
			zap.Int("attempts", attemptCount),
			zap.String("error", lastError))
		return nil
	}

	// Exponential backoff: 30s * 2^(attempt-1), capped at 1 hour
	backoffSec := 30 * (1 << (attemptCount - 1))
	if backoffSec > 3600 {
		backoffSec = 3600
	}

	_, err = r.db.ExecContext(ctx, `
		UPDATE media_index_outbox
		SET status = 'pending',
		    last_error = ?,
		    next_attempt_at = datetime('now', '+' || ? || ' seconds'),
		    updated_at = datetime('now')
		WHERE id = ?
	`, lastError, backoffSec, id)
	if err != nil {
		return fmt.Errorf("schedule retry for outbox %d: %w", id, err)
	}
	return nil
}

// ReclaimStale resets stale in_flight entries back to pending.
// Entries stuck in in_flight longer than staleThreshold are assumed abandoned.
func (r *Repository) ReclaimStale(ctx context.Context, staleThreshold time.Duration) (int64, error) {
	thresholdSec := int(staleThreshold.Seconds())
	result, err := r.db.ExecContext(ctx, `
		UPDATE media_index_outbox
		SET status = 'pending', updated_at = datetime('now')
		WHERE status = 'in_flight'
		  AND updated_at < datetime('now', '-' || ? || ' seconds')
	`, thresholdSec)
	if err != nil {
		return 0, fmt.Errorf("reclaim stale outbox entries: %w", err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

// CountByStatus returns the number of outbox entries in a single status bucket.
// Used by the PR3-5b IndexHealth cross-check to report `pending_outbox` and
// `dead_letter` counts without scanning the whole table.
//
// An empty `status` argument is rejected up-front (the SQL `WHERE status = ”`
// would silently return 0, masking a programming error). An invalid status
// value (e.g. `"intentional_typo"`) is a programmer error too — but the SQL
// simply returns 0 rather than failing, so prefer calling this with the
// literal constants ("pending", "in_flight", "processed", "dead_letter").
func (r *Repository) CountByStatus(ctx context.Context, status string) (int64, error) {
	if r.db == nil {
		return 0, fmt.Errorf("outbox.Repository: db is nil")
	}
	if status == "" {
		return 0, fmt.Errorf("outbox.CountByStatus: status is required")
	}
	var n int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM media_index_outbox WHERE status = ?", status,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("outbox.CountByStatus(%q): %w", status, err)
	}
	return n, nil
}

// PendingCount returns the number of entries in each status bucket.
func (r *Repository) PendingCount(ctx context.Context) (map[string]int, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT status, COUNT(*) FROM media_index_outbox GROUP BY status
	`)
	if err != nil {
		return nil, fmt.Errorf("count outbox entries: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			continue
		}
		counts[status] = count
	}
	return counts, nil
}

// OldestPendingAge returns the age of the oldest pending entry as a Duration.
// Returns 0 if no pending entries exist.
func (r *Repository) OldestPendingAge(ctx context.Context) (time.Duration, error) {
	var createdAt string
	err := r.db.QueryRowContext(ctx, `
		SELECT created_at FROM media_index_outbox
		WHERE status = 'pending'
		ORDER BY created_at ASC LIMIT 1
	`).Scan(&createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, fmt.Errorf("get oldest pending age: %w", err)
	}
	t, err := time.Parse("2006-01-02 15:04:05", createdAt)
	if err != nil {
		return 0, nil
	}
	return time.Since(t), nil
}
