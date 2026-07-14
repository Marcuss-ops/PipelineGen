package assets

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// MarkEnqueued flips the ledger row from `pending` → `enqueued`.
// Idempotent on repeated calls: a row with state='enqueued' stays
// at 'enqueued', enqueued_at stays at the FIRST successful
// enqueue's timestamp — guarantees the watermark doesn't oscillate
// between cycles on retry-after-transient-error paths.
//
// Returns:
//   - nil: TransitionApplied — the row was in 'pending'/'analyzing'
//     and is now 'enqueued'.
//   - ErrAlreadyApplied: the row is already 'enqueued' — idempotent,
//     not an error.
//   - ErrNotFound: no row exists with the given id.
//   - ErrStateConflict: the row exists but its state is not
//     'pending'/'analyzing' (e.g. 'rejected_terminal').
//
// Blocco 2 (July 2026) — FASE 1.3: ErrNotFound is now surfaced
// distinctly from ErrStateConflict. Pre-fix, the sql.ErrNoRows from
// the state-check query was wrapped as ErrStateConflict, making
// "row not found" indistinguishable from "wrong state".
func (r *YoutubeDiscoveriesRepository) MarkEnqueued(ctx context.Context, id, enqueuedAt string) error {
	if id == "" {
		return fmt.Errorf("youtube_discoveries.MarkEnqueued: id is required")
	}
	if enqueuedAt == "" {
		enqueuedAt = r.now().UTC().Format(time.RFC3339)
	}
	nowStr := r.now().UTC().Format(time.RFC3339)
	res, err := r.db.ExecContext(ctx, `
		UPDATE youtube_discoveries
		SET state = 'enqueued',
		    enqueued_at = ?,
		    outcome = 'enqueued',
		    updated_at = ?
		WHERE id = ? AND state IN ('pending', 'analyzing')
	`, enqueuedAt, nowStr, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// RowsAffected == 0 — the row is either already enqueued,
		// in a conflicting state, or doesn't exist.
		var gotState string
		if scanErr := r.db.QueryRowContext(ctx, `SELECT state FROM youtube_discoveries WHERE id = ?`, id).Scan(&gotState); scanErr != nil {
			if scanErr == sql.ErrNoRows {
				return fmt.Errorf("%w: MarkEnqueued row not found for id=%q", ErrNotFound, id)
			}
			return fmt.Errorf("youtube_discoveries.MarkEnqueued: state lookup for id=%q: %w", id, scanErr)
		}
		if gotState == "enqueued" {
			return ErrAlreadyApplied
		}
		return fmt.Errorf("%w: MarkEnqueued expected state IN ('pending','analyzing'), got %q for id=%q", ErrStateConflict, gotState, id)
	}
	return nil
}

// MarkRejected records an explicit rejection on the ledger row.
//
// retryable=true → state='rejected_retryable', next_retry_at=
// now+backoff(attempt_count+1), attempt_count+=1, last_error pinned.
// retryable=false → state='rejected_terminal', last_error pinned,
// attempt_count unchanged (terminal — no further retries).
//
// Both paths preserve the row's audit trail (rejection_reason +
// legacy outcome column shadow). Caller is the monitor package's
// enqueue.go where retryable is computed from isTransientErr.
//
// Blocco 2 (July 2026): the retryable path replaces the pre-Blocco-2
// SELECT attempt_count + UPDATE (two queries, non-atomic) with a
// single atomic UPDATE ... SET attempt_count = attempt_count + 1 ...
// RETURNING attempt_count. The RETURNING clause surfaces the
// post-increment value atomically; the follow-up UPDATE sets
// next_retry_at from the known returned count. Both paths check
// RowsAffected and return ErrStateConflict on zero rows (audit P0 #3).
func (r *YoutubeDiscoveriesRepository) MarkRejected(ctx context.Context, id, rejectionReason string, retryable bool) error {
	if id == "" {
		return fmt.Errorf("youtube_discoveries.MarkRejected: id is required")
	}
	nowStr := r.now().UTC().Format(time.RFC3339)
	if retryable {
		// Atomic UPDATE: bump attempt_count in SQL (no separate SELECT).
		// RETURNING gives us the post-increment value so we can compute
		// the backoff without a race window.
		//
		// Blocco 2 crash-recovery hardening: the two UPDATEs (state bump
		// + next_retry_at) run inside a single SQLite transaction so a
		// crash between them cannot leave the row at
		// state='rejected_retryable' with next_retry_at=NULL — that
		// state is permanently unreclaimable by tryReserveConflict(b).
		tx, txErr := r.db.BeginTx(ctx, nil)
		if txErr != nil {
			return fmt.Errorf("youtube_discoveries.MarkRejected: begin tx: %w", txErr)
		}
		defer func() {
			if tx != nil {
				_ = tx.Rollback()
			}
		}()

		var newAttempt sql.NullInt64
		row := tx.QueryRowContext(ctx, `
			UPDATE youtube_discoveries
			SET state = 'rejected_retryable',
			    last_error = ?,
			    rejection_reason = ?,
			    outcome = 'rejected',
			    attempt_count = attempt_count + 1,
			    updated_at = ?
			WHERE id = ? AND state IN ('pending', 'analyzing')
			RETURNING attempt_count
		`, rejectionReason, rejectionReason, nowStr, id)
		if err := row.Scan(&newAttempt); err != nil {
			if err == sql.ErrNoRows {
				// Distinguish not-found from state conflict.
				var exists int
				if scanErr := tx.QueryRowContext(ctx, `SELECT 1 FROM youtube_discoveries WHERE id = ?`, id).Scan(&exists); scanErr != nil {
					if scanErr == sql.ErrNoRows {
						return fmt.Errorf("%w: MarkRejected(retryable) row not found for id=%q", ErrNotFound, id)
					}
					return fmt.Errorf("youtube_discoveries.MarkRejected: existence check: %w", scanErr)
				}
				return fmt.Errorf("%w: MarkRejected(retryable) expected state IN ('pending','analyzing') for id=%q", ErrStateConflict, id)
			}
			return fmt.Errorf("youtube_discoveries.MarkRejected: retryable update: %w", err)
		}
		// Set next_retry_at from the atomically-returned count.
		retryAtStr := r.now().UTC().Add(time.Duration(ComputeRetryBackoffSeconds(int(newAttempt.Int64))) * time.Second).Format(time.RFC3339)
		if _, err := tx.ExecContext(ctx, `
			UPDATE youtube_discoveries
			SET next_retry_at = ?
			WHERE id = ?
		`, retryAtStr, id); err != nil {
			return fmt.Errorf("youtube_discoveries.MarkRejected: set next_retry_at: %w", err)
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return fmt.Errorf("youtube_discoveries.MarkRejected: commit: %w", commitErr)
		}
		tx = nil // disable rollback in defer
		return nil
	}
	// Terminal path: no retry, attempt_count stays as-is.
	// Blocco 2: include 'rejected_retryable' so the caller can escalate
	// a transient rejection to terminal (valid path: pending/analyzing/
	// rejected_retryable → rejected_terminal).
	res, err := r.db.ExecContext(ctx, `
		UPDATE youtube_discoveries
		SET state = 'rejected_terminal',
		    last_error = ?,
		    rejection_reason = ?,
		    outcome = 'rejected',
		    updated_at = ?
		WHERE id = ? AND state IN ('pending', 'analyzing', 'rejected_retryable')
	`, rejectionReason, rejectionReason, nowStr, id)
	if err != nil {
		return fmt.Errorf("youtube_discoveries.MarkRejected: terminal update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Distinguish not-found from state conflict.
		var exists int
		if scanErr := r.db.QueryRowContext(ctx, `SELECT 1 FROM youtube_discoveries WHERE id = ?`, id).Scan(&exists); scanErr != nil {
			if scanErr == sql.ErrNoRows {
				return fmt.Errorf("%w: MarkRejected(terminal) row not found for id=%q", ErrNotFound, id)
			}
			return fmt.Errorf("youtube_discoveries.MarkRejected: existence check: %w", scanErr)
		}
		return fmt.Errorf("%w: MarkRejected(terminal) expected state IN ('pending','analyzing','rejected_retryable') for id=%q", ErrStateConflict, id)
	}
	return nil
}

// MarkReclaimByLease reclaims expired pending/analyzing leases for
// a given lease_owner within the channel. Returns the count of
// reclaimed rows. Used by the scheduler's lease-expiry reclaim
// path (currently exercised only by tests; the production
// multi-instance dispatcher is a future commit).
func (r *YoutubeDiscoveriesRepository) MarkReclaimByLease(
	ctx context.Context,
	leaseOwner, nowStr string,
) (int, error) {
	if leaseOwner == "" {
		return 0, fmt.Errorf("youtube_discoveries.MarkReclaimByLease: leaseOwner is required")
	}
	if nowStr == "" {
		nowStr = r.now().UTC().Format(time.RFC3339)
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE youtube_discoveries
		SET state = 'pending',
		    lease_owner = NULL,
		    lease_until = NULL,
		    updated_at = ?
		WHERE lease_owner = ?
		  AND lease_until IS NOT NULL
		  AND lease_until < ?
		  AND state IN ('pending', 'analyzing')
	`, nowStr, leaseOwner, nowStr)
	if err != nil {
		return 0, fmt.Errorf("youtube_discoveries.MarkReclaimByLease: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
