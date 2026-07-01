// internal/infrastructure/database/sqlite/assets/monitor_outbox.go
// ──────────────────────────────────────────────────────────────────────────────
// Monitor enqueue outbox — Blocco 3 (July 2026, audit P0 #2).
//
// The outbox solves the torn-write problem between the durable-jobs broker
// and the youtube_discoveries ledger: pre-Blocco-3, the monitor first
// emitted a job via EnqueueExtract, then called MarkEnqueued on the ledger.
// When MarkEnqueued failed, the broker had the job but the ledger stayed
// 'pending' — a silent-success path that could cause duplicate emissions.
//
// Blocco 3 introduces a local outbox table in the same SQLite database:
//
//   - CommitEnqueueOutbox(ctx, discoveryID, enqueuedAt, idempotencyKey,
//     payloadJSON) atomically runs MarkEnqueued + INSERT into
//     monitor_enqueue_outbox in a single transaction. Both succeed or
//     both fail — no torn write.
//
//   - A background drainer (startOutboxDrainer in the monitor scheduler)
//     polls pending outbox entries and dispatches them to the durable-jobs
//     broker via the canonical JobEnqueuer port.
//
//   - On successful broker emit, MarkOutboxDispatched records the job ID.
//   - On failure, MarkOutboxFailed records the error so the operator's
//     dashboard can surface undelivered outbox entries.
//
// The idempotency key is `youtube-extract:{discovery_id}:{policy_version}`
// and is enforced via UNIQUE constraint — the same (discovery_id,
// policy_version) pair cannot produce duplicate outbox entries.

package assets

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// OutboxEntry is the canonical shape surfaced to the drainer.
// It carries enough information to reconstruct the ExtractRequest
// payload the durable-jobs broker expects.
type OutboxEntry struct {
	ID             int64  `json:"id"`
	DiscoveryID    string `json:"discovery_id"`
	IdempotencyKey string `json:"idempotency_key"`
	PayloadJSON    string `json:"payload_json"`
}

// ensureOutboxTable creates the outbox table if it doesn't exist.
// Called from CommitEnqueueOutbox on first use (CREATE TABLE IF NOT EXISTS
// is idempotent and cheap — no separate migration runner needed).
func ensureOutboxTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS monitor_enqueue_outbox (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			discovery_id      TEXT NOT NULL,
			idempotency_key   TEXT NOT NULL UNIQUE,
			payload_json      TEXT NOT NULL,
			state             TEXT NOT NULL DEFAULT 'pending',
			created_at        TEXT NOT NULL DEFAULT (datetime('now')),
			dispatched_at     TEXT,
			job_id            TEXT,
			error             TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_monitor_outbox_pending
			ON monitor_enqueue_outbox(state, created_at)
			WHERE state = 'pending';
	`)
	return err
}

// CommitEnqueueOutbox atomically marks the discovery as enqueued AND inserts
// a pending outbox entry. Both operations run in a single SQLite transaction.
//
// Returns ErrDuplicateOutboxKey when the idempotency_key already exists
// (the caller should treat this as idempotent — the outbox entry was
// already committed in a prior attempt).
var ErrDuplicateOutboxKey = fmt.Errorf("monitor_outbox: duplicate idempotency key")

func (r *YoutubeDiscoveriesRepository) CommitEnqueueOutbox(
	ctx context.Context,
	discoveryID, enqueuedAt, idempotencyKey, payloadJSON string,
) error {
	if discoveryID == "" || idempotencyKey == "" {
		return fmt.Errorf("monitor_outbox.CommitEnqueueOutbox: discoveryID and idempotencyKey are required")
	}
	if enqueuedAt == "" {
		enqueuedAt = time.Now().UTC().Format(time.RFC3339)
	}

	if err := ensureOutboxTable(ctx, r.db); err != nil {
		return fmt.Errorf("monitor_outbox.CommitEnqueueOutbox: ensure table: %w", err)
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("monitor_outbox.CommitEnqueueOutbox: begin tx: %w", err)
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	// 1. MarkEnqueued within the transaction.
	res, err := tx.ExecContext(ctx, `
		UPDATE youtube_discoveries
		SET state = 'enqueued',
		    enqueued_at = ?,
		    outcome = 'enqueued',
		    updated_at = ?
		WHERE id = ? AND state IN ('pending', 'analyzing')
	`, enqueuedAt, nowStr, discoveryID)
	if err != nil {
		return fmt.Errorf("monitor_outbox.CommitEnqueueOutbox: MarkEnqueued: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Check if already enqueued (idempotent) or state conflict.
		var gotState string
		if scanErr := tx.QueryRowContext(ctx, `SELECT state FROM youtube_discoveries WHERE id = ?`, discoveryID).Scan(&gotState); scanErr != nil {
			return fmt.Errorf("%w: CommitEnqueueOutbox row not found for id=%q: %w", ErrStateConflict, discoveryID, scanErr)
		}
		if gotState == "enqueued" {
			// Row already enqueued — the outbox insert was also
			// committed in a prior attempt. Commit the current
			// tx (MarkEnqueued of already-enqueued is idempotent)
			// so the UPDATE survives and the caller sees success.
			if commitErr := tx.Commit(); commitErr != nil {
				return fmt.Errorf("monitor_outbox.CommitEnqueueOutbox: commit on idempotent: %w", commitErr)
			}
			tx = nil // disable rollback in defer
			return nil
		}
		return fmt.Errorf("%w: CommitEnqueueOutbox expected state IN ('pending','analyzing'), got %q for id=%q", ErrStateConflict, gotState, discoveryID)
	}

	// 2. Insert outbox entry within the same transaction.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO monitor_enqueue_outbox (discovery_id, idempotency_key, payload_json)
		VALUES (?, ?, ?)
	`, discoveryID, idempotencyKey, payloadJSON)
	if err != nil {
		// UNIQUE constraint violation → duplicate key (idempotent).
		if isUniqueConstraint(err) {
			// Outbox entry already exists from a prior successful
			// CommitEnqueueOutbox call. Commit the current tx so
			// the MarkEnqueued UPDATE survives.
			if commitErr := tx.Commit(); commitErr != nil {
				return fmt.Errorf("monitor_outbox.CommitEnqueueOutbox: commit on dup outbox: %w", commitErr)
			}
			tx = nil // disable rollback in defer
			return nil // already committed in a prior attempt
		}
		return fmt.Errorf("monitor_outbox.CommitEnqueueOutbox: insert outbox: %w", err)
	}

	if commitErr := tx.Commit(); commitErr != nil {
		return fmt.Errorf("monitor_outbox.CommitEnqueueOutbox: commit: %w", commitErr)
	}
	tx = nil // disable rollback in defer
	return nil
}

// DrainPendingOutbox returns up to limit pending outbox entries ordered by
// created_at ASC. The caller (the outbox drainer goroutine) dispatches
// each entry to the durable-jobs broker, then calls MarkOutboxDispatched
// or MarkOutboxFailed.
func (r *YoutubeDiscoveriesRepository) DrainPendingOutbox(ctx context.Context, limit int) ([]OutboxEntry, error) {
	if limit <= 0 {
		limit = 10
	}
	if err := ensureOutboxTable(ctx, r.db); err != nil {
		return nil, fmt.Errorf("monitor_outbox.DrainPendingOutbox: ensure table: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, discovery_id, idempotency_key, payload_json
		FROM monitor_enqueue_outbox
		WHERE state = 'pending'
		ORDER BY created_at ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("monitor_outbox.DrainPendingOutbox: query: %w", err)
	}
	defer rows.Close()

	var entries []OutboxEntry
	for rows.Next() {
		var e OutboxEntry
		if scanErr := rows.Scan(&e.ID, &e.DiscoveryID, &e.IdempotencyKey, &e.PayloadJSON); scanErr != nil {
			return nil, fmt.Errorf("monitor_outbox.DrainPendingOutbox: scan: %w", scanErr)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// MarkOutboxDispatched marks an outbox entry as successfully dispatched
// with the resulting durable-job ID.
func (r *YoutubeDiscoveriesRepository) MarkOutboxDispatched(ctx context.Context, outboxID int64, jobID string) error {
	nowStr := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.ExecContext(ctx, `
		UPDATE monitor_enqueue_outbox
		SET state = 'dispatched',
		    job_id = ?,
		    dispatched_at = ?
		WHERE id = ? AND state = 'pending'
	`, jobID, nowStr, outboxID)
	if err != nil {
		return fmt.Errorf("monitor_outbox.MarkOutboxDispatched: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("monitor_outbox.MarkOutboxDispatched: outbox entry %d not found or not pending", outboxID)
	}
	return nil
}

// MarkOutboxFailed marks an outbox entry as failed with an error message.
// Failed entries remain in the table for operator visibility but are no
// longer picked up by DrainPendingOutbox.
func (r *YoutubeDiscoveriesRepository) MarkOutboxFailed(ctx context.Context, outboxID int64, errMsg string) error {
	nowStr := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.ExecContext(ctx, `
		UPDATE monitor_enqueue_outbox
		SET state = 'failed',
		    error = ?,
		    dispatched_at = ?
		WHERE id = ? AND state = 'pending'
	`, errMsg, nowStr, outboxID)
	if err != nil {
		return fmt.Errorf("monitor_outbox.MarkOutboxFailed: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("monitor_outbox.MarkOutboxFailed: outbox entry %d not found or not pending", outboxID)
	}
	return nil
}

// isUniqueConstraint returns true when the error is a SQLite UNIQUE
// constraint violation. Used by CommitEnqueueOutbox to detect
// duplicate idempotency_key.
func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	// SQLite returns "UNIQUE constraint failed: <details>" on violation.
	return containsFold(err.Error(), "UNIQUE constraint")
}

func containsFold(s, substr string) bool {
	return len(s) >= len(substr) && strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
