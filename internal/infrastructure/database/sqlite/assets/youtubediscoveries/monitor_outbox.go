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
//
// Step 7/12 (July 2026): added RetryCount, NextRetryAt, LeaseID,
// LeaseUntil to support the lease-based atomic claim + retryable
// failure pattern.
type OutboxEntry struct {
	ID             int64  `json:"id"`
	DiscoveryID    string `json:"discovery_id"`
	IdempotencyKey string `json:"idempotency_key"`
	PayloadJSON    string `json:"payload_json"`
	State          string `json:"state"`
	RetryCount     int    `json:"retry_count"`
	NextRetryAt    string `json:"next_retry_at,omitempty"`
}

// ensureOutboxTable is now a no-op — the table is created by the
// canonical migration 120_monitor_enqueue_outbox_lease.sql (Step 7/12).
// Kept as a compatibility shim so callers still compile; removed in a
// follow-up cleanup wave.
func ensureOutboxTable(ctx context.Context, db *sql.DB) error {
	return nil
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
			tx = nil   // disable rollback in defer
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

// DrainPendingOutbox atomically claims up to limit pending outbox entries
// using a SELECT → UPDATE → SELECT transaction. The caller receives
// exclusively claimed rows in 'dispatching' state with the supplied
// lease. Rows with next_retry_at in the future are skipped (backoff
// not yet elapsed).
//
// Step 7/12: replaced the pre-migration SELECT-based query with an
// atomic claim that prevents two drainers from reading the same row.
//
// July 2026: replaced UPDATE ... RETURNING (unsupported on SQLite 3.37)
// with an explicit transaction that SELECTs candidate IDs, UPDATEs them,
// and SELECTs the full rows — all within a single BEGIN/COMMIT.
func (r *YoutubeDiscoveriesRepository) DrainPendingOutbox(ctx context.Context, limit int, leaseID, leaseUntil string) ([]OutboxEntry, error) {
	if limit <= 0 {
		limit = 10
	}
	if err := ensureOutboxTable(ctx, r.db); err != nil {
		return nil, fmt.Errorf("monitor_outbox.DrainPendingOutbox: ensure table: %w", err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("monitor_outbox.DrainPendingOutbox: begin tx: %w", err)
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	// Step 1: SELECT candidate IDs within the transaction.
	idRows, err := tx.QueryContext(ctx, `
		SELECT id FROM monitor_enqueue_outbox
		WHERE state = 'pending'
		  AND (next_retry_at IS NULL OR next_retry_at <= datetime('now'))
		ORDER BY created_at ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("monitor_outbox.DrainPendingOutbox: select ids: %w", err)
	}
	var ids []int64
	for idRows.Next() {
		var id int64
		if scanErr := idRows.Scan(&id); scanErr != nil {
			idRows.Close()
			return nil, fmt.Errorf("monitor_outbox.DrainPendingOutbox: scan id: %w", scanErr)
		}
		ids = append(ids, id)
	}
	idRows.Close()
	if err := idRows.Err(); err != nil {
		return nil, fmt.Errorf("monitor_outbox.DrainPendingOutbox: idRows: %w", err)
	}

	if len(ids) == 0 {
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, fmt.Errorf("monitor_outbox.DrainPendingOutbox: commit empty: %w", commitErr)
		}
		tx = nil
		return nil, nil
	}

	// Step 2: UPDATE the selected rows.
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+2)
	args = append(args, leaseID, leaseUntil)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	updateQuery := `UPDATE monitor_enqueue_outbox
		SET state = 'dispatching', lease_id = ?, lease_until = ?
		WHERE id IN (` + strings.Join(placeholders, ",") + `)`
	if _, err := tx.ExecContext(ctx, updateQuery, args...); err != nil {
		return nil, fmt.Errorf("monitor_outbox.DrainPendingOutbox: update: %w", err)
	}

	// Step 3: SELECT the full rows for the caller.
	selectArgs := make([]any, len(ids))
	for i, id := range ids {
		selectArgs[i] = id
	}
	selectQuery := `SELECT id, discovery_id, idempotency_key, payload_json, state, retry_count, COALESCE(next_retry_at, '')
		FROM monitor_enqueue_outbox WHERE id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := tx.QueryContext(ctx, selectQuery, selectArgs...)
	if err != nil {
		return nil, fmt.Errorf("monitor_outbox.DrainPendingOutbox: select rows: %w", err)
	}
	defer rows.Close()

	var entries []OutboxEntry
	for rows.Next() {
		var e OutboxEntry
		if scanErr := rows.Scan(&e.ID, &e.DiscoveryID, &e.IdempotencyKey, &e.PayloadJSON, &e.State, &e.RetryCount, &e.NextRetryAt); scanErr != nil {
			return nil, fmt.Errorf("monitor_outbox.DrainPendingOutbox: scan: %w", scanErr)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("monitor_outbox.DrainPendingOutbox: rows: %w", err)
	}

	if commitErr := tx.Commit(); commitErr != nil {
		return nil, fmt.Errorf("monitor_outbox.DrainPendingOutbox: commit: %w", commitErr)
	}
	tx = nil
	return entries, nil
}

// DrainDispatched reclaims rows stuck in 'dispatching' state with
// expired leases. On reclamation, the row is reset to 'pending' with
// cleared lease so it can be picked up by DrainPendingOutbox.
//
// Step 7/12: prevents permanent row loss when a drainer crashes mid-
// dispatch.
//
// July 2026: replaced UPDATE ... RETURNING (unsupported on SQLite 3.37)
// with an explicit transaction (same pattern as DrainPendingOutbox).
func (r *YoutubeDiscoveriesRepository) DrainDispatched(ctx context.Context, limit int, leaseID, leaseUntil string) ([]OutboxEntry, error) {
	if limit <= 0 {
		limit = 10
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("monitor_outbox.DrainDispatched: begin tx: %w", err)
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	// Step 1: SELECT candidate IDs within the transaction.
	idRows, err := tx.QueryContext(ctx, `
		SELECT id FROM monitor_enqueue_outbox
		WHERE state = 'dispatching'
		  AND lease_until < datetime('now')
		ORDER BY created_at ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("monitor_outbox.DrainDispatched: select ids: %w", err)
	}
	var ids []int64
	for idRows.Next() {
		var id int64
		if scanErr := idRows.Scan(&id); scanErr != nil {
			idRows.Close()
			return nil, fmt.Errorf("monitor_outbox.DrainDispatched: scan id: %w", scanErr)
		}
		ids = append(ids, id)
	}
	idRows.Close()
	if err := idRows.Err(); err != nil {
		return nil, fmt.Errorf("monitor_outbox.DrainDispatched: idRows: %w", err)
	}

	if len(ids) == 0 {
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, fmt.Errorf("monitor_outbox.DrainDispatched: commit empty: %w", commitErr)
		}
		tx = nil
		return nil, nil
	}

	// Step 2: UPDATE the selected rows — reset to pending + clear lease.
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	updateQuery := `UPDATE monitor_enqueue_outbox
		SET state = 'pending', lease_id = '', lease_until = NULL
		WHERE id IN (` + strings.Join(placeholders, ",") + `)`
	if _, err := tx.ExecContext(ctx, updateQuery, args...); err != nil {
		return nil, fmt.Errorf("monitor_outbox.DrainDispatched: reclaim: %w", err)
	}

	// Step 3: SELECT the full rows for the caller.
	selectArgs := make([]any, len(ids))
	for i, id := range ids {
		selectArgs[i] = id
	}
	selectQuery := `SELECT id, discovery_id, idempotency_key, payload_json, state, retry_count, COALESCE(next_retry_at, '')
		FROM monitor_enqueue_outbox WHERE id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := tx.QueryContext(ctx, selectQuery, selectArgs...)
	if err != nil {
		return nil, fmt.Errorf("monitor_outbox.DrainDispatched: select rows: %w", err)
	}
	defer rows.Close()

	var entries []OutboxEntry
	for rows.Next() {
		var e OutboxEntry
		if scanErr := rows.Scan(&e.ID, &e.DiscoveryID, &e.IdempotencyKey, &e.PayloadJSON, &e.State, &e.RetryCount, &e.NextRetryAt); scanErr != nil {
			return nil, fmt.Errorf("monitor_outbox.DrainDispatched: scan: %w", scanErr)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("monitor_outbox.DrainDispatched: rows: %w", err)
	}

	if commitErr := tx.Commit(); commitErr != nil {
		return nil, fmt.Errorf("monitor_outbox.DrainDispatched: commit: %w", commitErr)
	}
	tx = nil
	return entries, nil
}

// MarkOutboxDispatched marks an outbox entry as successfully dispatched
// with the resulting durable-job ID. Requires the row to be in
// 'dispatching' state (the drainer claims it before dispatching).
func (r *YoutubeDiscoveriesRepository) MarkOutboxDispatched(ctx context.Context, outboxID int64, jobID string) error {
	nowStr := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.ExecContext(ctx, `
		UPDATE monitor_enqueue_outbox
		SET state = 'dispatched',
		    job_id = ?,
		    dispatched_at = ?,
		    lease_id = '',
		    lease_until = NULL
		WHERE id = ? AND state = 'dispatching'
	`, jobID, nowStr, outboxID)
	if err != nil {
		return fmt.Errorf("monitor_outbox.MarkOutboxDispatched: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("monitor_outbox.MarkOutboxDispatched: outbox entry %d not found or not dispatching", outboxID)
	}
	return nil
}

// maxOutboxRetries is the number of retry attempts before an outbox
// entry is marked dead (terminal failure).
const maxOutboxRetries = 3

// MarkOutboxFailed records a transient failure and reschedules the
// outbox entry for retry. The entry is set back to 'pending' with
// next_retry_at computed via exponential backoff (5s, 15s, 45s).
// After maxOutboxRetries consecutive failures, the entry is marked
// 'dead' (terminal — operator must manually intervene).
//
// Step 7/12: replaces the pre-migration permanent 'failed' state
// with retryable pending + dead letter after N retries.
func (r *YoutubeDiscoveriesRepository) MarkOutboxFailed(ctx context.Context, outboxID int64, errMsg string) error {
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	// Read current retry_count to decide pending vs dead.
	var retryCount int
	scanErr := r.db.QueryRowContext(ctx,
		`SELECT retry_count FROM monitor_enqueue_outbox WHERE id = ?`, outboxID,
	).Scan(&retryCount)
	if scanErr != nil {
		return fmt.Errorf("monitor_outbox.MarkOutboxFailed: read retry_count for %d: %w", outboxID, scanErr)
	}

	newRetryCount := retryCount + 1

	if newRetryCount >= maxOutboxRetries {
		// Terminal: mark as dead.
		res, err := r.db.ExecContext(ctx, `
			UPDATE monitor_enqueue_outbox
			SET state = 'dead',
			    error = ?,
			    dispatched_at = ?,
			    retry_count = ?,
			    lease_id = '',
			    lease_until = NULL
			WHERE id = ? AND state = 'dispatching'
		`, errMsg, nowStr, newRetryCount, outboxID)
		if err != nil {
			return fmt.Errorf("monitor_outbox.MarkOutboxFailed: dead letter: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("monitor_outbox.MarkOutboxFailed: outbox entry %d not found or not dispatching", outboxID)
		}
		return nil
	}

	// Retryable: set back to pending with exponential backoff.
	backoff := time.Duration(5*newRetryCount) * time.Second
	nextRetryAt := now.Add(backoff).Format(time.RFC3339)

	res, err := r.db.ExecContext(ctx, `
		UPDATE monitor_enqueue_outbox
		SET state = 'pending',
		    error = ?,
		    next_retry_at = ?,
		    retry_count = ?,
		    lease_id = '',
		    lease_until = NULL
		WHERE id = ? AND state = 'dispatching'
	`, errMsg, nextRetryAt, newRetryCount, outboxID)
	if err != nil {
		return fmt.Errorf("monitor_outbox.MarkOutboxFailed: retryable: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("monitor_outbox.MarkOutboxFailed: outbox entry %d not found or not dispatching", outboxID)
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
