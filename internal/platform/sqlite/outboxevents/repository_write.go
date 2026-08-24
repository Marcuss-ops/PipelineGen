// Write/lifecycle methods for the outbox Repository. This is the hot-path
// surface — every IndexClip + drive_delete + index_delete + delivery
// worker routes through these methods. The lease-fenced UPDATE pattern
// (status='processing' AND lease_id=?) is the canonical anti-stale-consumer
// guard and MUST NOT be relaxed.
//
// Per godlike/06 SSOT one-canonical-owner-per-fact, every state transition
// has exactly one writer here:
//
//	pending     -> processing    : ClaimNext
//	processing  -> processing    : RenewLease
//	processing  -> completed     : MarkCompleted
//	processing  -> pending       : MarkFailed (attempts remaining)
//	processing  -> dead_letter   : MarkFailed (attempts exhausted) OR MarkDeadLetter (terminal error reported by handler)
//	processing  -> superseded    : MarkSuperseded (handler reported *SupersedeError)
//	processing  -> pending       : RequeueExpiredLeases (lease TTL expired)
package outboxevents

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"github.com/google/uuid"
)

// Enqueue inserts a new outbox event at the default normal priority.
// Call this inside a transaction. Uses ON CONFLICT(event_key) DO NOTHING
// for idempotency.
//
// The INSERT intentionally does NOT reference the priority column so
// legacy minimal fixtures (and repositories that predate migration 186)
// keep working — the column default (PriorityNormal) applies. Producers
// that need script-required ordering must use EnqueueWithPriority.
//
// Returns EnqueueResult with Inserted=true + EventID when the INSERT
// lands. When ON CONFLICT suppresses the insert, returns Inserted=false
// with the existing row's ID and status so the producer can distinguish
// "fresh event" from "suppressed by existing completed/dead_letter/
// superseded row".
func (r *Repository) Enqueue(ctx context.Context, tx *sql.Tx, eventType, aggregateID, aggregateType, payloadJSON, eventKey string) (*EnqueueResult, error) {
	now := timeutil.FormatRFC3339(time.Now())
	exec := r.exec(ctx, tx)
	result, err := exec(ctx,
		`INSERT INTO outbox_events (event_type, aggregate_id, aggregate_type, payload_json, event_key, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING`,
		eventType, aggregateID, aggregateType, payloadJSON, eventKey, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("outboxevents.Enqueue(%s, %s): %w", eventType, aggregateID, err)
	}
	affected, _ := result.RowsAffected()
	if affected > 0 {
		id, _ := result.LastInsertId()
		return &EnqueueResult{EventID: id, Inserted: true}, nil
	}
	// ON CONFLICT suppressed the insert — query the existing row's
	// status so the producer knows whether the event was squelched
	// by a completed, dead_letter, or superseded row.
	//
	// IMPORTANT: use the same handle as the INSERT (tx when provided,
	// db otherwise). A detached r.db.QueryRowContext can open a NEW
	// connection to :memory:, which sees an entirely different database.
	// With SetMaxOpenConns(1) this deadlocks (the only connection is
	// already held by the caller's tx).
	var existingID int64
	var existingStatus, existingEventType, existingAggregateType, existingAggregateID string
	queryRow := r.db.QueryRowContext
	if tx != nil {
		queryRow = tx.QueryRowContext
	}
	if scanErr := queryRow(ctx,
		`SELECT id, status, event_type, aggregate_type, aggregate_id FROM outbox_events WHERE event_key = ?`, eventKey,
	).Scan(&existingID, &existingStatus, &existingEventType, &existingAggregateType, &existingAggregateID); scanErr != nil {
		return nil, fmt.Errorf("outboxevents.Enqueue(%s, %s): ON CONFLICT suppressed, but query existing row: %w", eventType, aggregateID, scanErr)
	}
	return &EnqueueResult{EventID: existingID, Inserted: false, ExistingStatus: existingStatus, ExistingEventType: existingEventType, ExistingAggregateType: existingAggregateType, ExistingAggregateID: existingAggregateID}, nil
}

// EnqueueWithPriority inserts a new outbox event with an explicit
// scheduling priority (migration 186). ClaimNext claims higher
// priorities first, so a script-required asset.index.requested can
// jump ahead of a bulk-folder-sync backlog. All other semantics are
// identical to Enqueue (idempotent ON CONFLICT(event_key)).
//
// Requires the backing table to carry the priority column (migration
// 186). In-memory test fixtures that omit the column must use Enqueue
// (column default applies) or add the column to their CREATE TABLE.
func (r *Repository) EnqueueWithPriority(ctx context.Context, tx *sql.Tx, eventType, aggregateID, aggregateType, payloadJSON, eventKey string, priority int) (*EnqueueResult, error) {
	now := timeutil.FormatRFC3339(time.Now())
	exec := r.exec(ctx, tx)
	result, err := exec(ctx,
		`INSERT INTO outbox_events (event_type, aggregate_id, aggregate_type, payload_json, event_key, priority, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING`,
		eventType, aggregateID, aggregateType, payloadJSON, eventKey, priority, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("outboxevents.EnqueueWithPriority(%s, %s): %w", eventType, aggregateID, err)
	}
	affected, _ := result.RowsAffected()
	if affected > 0 {
		id, _ := result.LastInsertId()
		return &EnqueueResult{EventID: id, Inserted: true}, nil
	}
	// ON CONFLICT suppressed the insert — query the existing row's
	// status so the producer knows whether the event was squelched
	// by a completed, dead_letter, or superseded row.
	//
	// IMPORTANT: use the same handle as the INSERT (tx when provided,
	// db otherwise). A detached r.db.QueryRowContext can open a NEW
	// connection to :memory:, which sees an entirely different database.
	// With SetMaxOpenConns(1) this deadlocks (the only connection is
	// already held by the caller's tx).
	var existingID int64
	var existingStatus, existingEventType, existingAggregateType, existingAggregateID string
	queryRow := r.db.QueryRowContext
	if tx != nil {
		queryRow = tx.QueryRowContext
	}
	if scanErr := queryRow(ctx,
		`SELECT id, status, event_type, aggregate_type, aggregate_id FROM outbox_events WHERE event_key = ?`, eventKey,
	).Scan(&existingID, &existingStatus, &existingEventType, &existingAggregateType, &existingAggregateID); scanErr != nil {
		return nil, fmt.Errorf("outboxevents.EnqueueWithPriority(%s, %s): ON CONFLICT suppressed, but query existing row: %w", eventType, aggregateID, scanErr)
	}
	return &EnqueueResult{EventID: existingID, Inserted: false, ExistingStatus: existingStatus, ExistingEventType: existingEventType, ExistingAggregateType: existingAggregateType, ExistingAggregateID: existingAggregateID}, nil
}

// ClaimNext claims the oldest pending event atomically using CTE.
func (r *Repository) ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration) (*Claim, error) {
	now := timeutil.FormatRFC3339(time.Now())
	leaseID := uuid.NewString()
	leaseExpiry := time.Now().Add(leaseTTL)
	leaseExpiryStr := timeutil.FormatRFC3339(leaseExpiry)

	// Atomic CTE claim: WITH candidate AS (...) UPDATE ... RETURNING id.
	// SQLite doesn't support RETURNING universally, so we use claim+refetch.
	// Claim ordering is (priority DESC, next_attempt_at ASC, id ASC): a
	// script-required index request (priority=10) is claimed before an
	// older bulk-folder-sync event (priority=5) even when the latter has
	// waited longer (migration 186).
	result, err := r.db.ExecContext(ctx, `
		WITH candidate AS (
			SELECT id FROM outbox_events
			WHERE status = 'pending'
			  AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
			ORDER BY priority DESC, next_attempt_at ASC, id ASC
			LIMIT 1
		)
		UPDATE outbox_events
		SET status = 'processing',
		    attempt_count = attempt_count + 1,
		    worker_id = ?, lease_id = ?, lease_expiry = ?,
		    updated_at = ?
		WHERE id = (SELECT id FROM candidate)
		  AND status = 'pending'
	`, now, workerID, leaseID, leaseExpiryStr, now)
	if err != nil {
		return nil, fmt.Errorf("outboxevents.ClaimNext: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return nil, nil
	}

	// Refetch the claimed event.
	row := r.db.QueryRowContext(ctx, `
		SELECT id, event_type, aggregate_id, aggregate_type, payload_json,
		       status, attempt_count, max_attempts, last_error,
		       event_key, worker_id, lease_id, lease_expiry, completed_at,
		       created_at, updated_at, priority
		FROM outbox_events
		WHERE worker_id = ? AND lease_id = ? AND status = 'processing'
		ORDER BY updated_at DESC LIMIT 1
	`, workerID, leaseID)
	evt, err := scanEvent(row)
	if err != nil {
		return nil, fmt.Errorf("outboxevents.ClaimNext refetch: %w", err)
	}
	return &Claim{Event: *evt, WorkerID: workerID, LeaseID: leaseID}, nil
}

// RenewLease extends the lease for a claimed event. Returns ErrLeaseLost
// if no row matches — either the event was re-assigned after lease expiry
// or the lease is already past its expiry (which would let a stale worker
// "resurrect" itself just before the reaper reassigns the event).
func (r *Repository) RenewLease(ctx context.Context, eventID int64, workerID, leaseID string, leaseTTL time.Duration) error {
	now := time.Now()
	newExpiry := now.Add(leaseTTL)
	result, err := r.db.ExecContext(ctx, `
		UPDATE outbox_events SET lease_expiry = ?, updated_at = ?
		WHERE id = ? AND status = 'processing'
		  AND worker_id = ? AND lease_id = ?
		  AND lease_expiry > ?`,
		timeutil.FormatRFC3339(newExpiry), timeutil.FormatRFC3339(now),
		eventID, workerID, leaseID, timeutil.FormatRFC3339(now))
	if err != nil {
		return fmt.Errorf("outboxevents.RenewLease: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("outboxevents.RenewLease(%d): %w", eventID, ErrLeaseLost)
	}
	return nil
}

// MarkCompleted marks an event as completed. Verifies status='processing'
// AND lease_id to prevent a stale consumer from completing an already
// re-assigned event. Returns ErrLeaseLost on mismatch.
func (r *Repository) MarkCompleted(ctx context.Context, eventID int64, leaseID string) error {
	now := timeutil.FormatRFC3339(time.Now())
	result, err := r.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = 'completed', completed_at = ?, updated_at = ?
		WHERE id = ? AND status = 'processing' AND lease_id = ?
	`, now, now, eventID, leaseID)
	if err != nil {
		return fmt.Errorf("outboxevents.MarkCompleted(%d): %w", eventID, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("outboxevents.MarkCompleted(%d): %w", eventID, ErrLeaseLost)
	}
	return nil
}

// MarkFailed handles a failed event. If attempts remain, goes back to pending
// with exponential backoff. If exhausted, goes to dead_letter.
// Verifies lease_id for fencing.
func (r *Repository) MarkFailed(ctx context.Context, eventID int64, leaseID string, errMsg string, nextAttemptAt time.Time) error {
	var attemptCount, maxAttempts int
	err := r.db.QueryRowContext(ctx,
		`SELECT attempt_count, max_attempts FROM outbox_events WHERE id = ?`, eventID,
	).Scan(&attemptCount, &maxAttempts)
	if err != nil {
		return fmt.Errorf("outboxevents.MarkFailed read: %w", err)
	}

	nowStr := timeutil.FormatRFC3339(time.Now())

	if attemptCount >= maxAttempts {
		// Dead letter. AND status='processing' prevents a stale consumer
		// whose lease_id has been recycled to a different row from
		// overwriting an already-terminal dead_letter row.
		result, err := r.db.ExecContext(ctx, `
			UPDATE outbox_events
			SET status = 'dead_letter', last_error = ?, updated_at = ?,
			    worker_id = '', lease_id = '', lease_expiry = NULL
			WHERE id = ? AND lease_id = ? AND status = 'processing'
		`, errMsg, nowStr, eventID, leaseID)
		if err != nil {
			return fmt.Errorf("outboxevents.MarkFailed dead_letter: %w", err)
		}
		affected, _ := result.RowsAffected()
		if affected == 0 {
			return fmt.Errorf("outboxevents.MarkFailed dead_letter: %w", ErrLeaseLost)
		}
	} else {
		result, err := r.db.ExecContext(ctx, `
			UPDATE outbox_events
			SET status = 'pending', next_attempt_at = ?, last_error = ?,
			    worker_id = '', lease_id = '', lease_expiry = NULL,
			    updated_at = ?
			WHERE id = ? AND lease_id = ? AND status = 'processing'
		`, timeutil.FormatRFC3339(nextAttemptAt), errMsg, nowStr, eventID, leaseID)
		if err != nil {
			return fmt.Errorf("outboxevents.MarkFailed pending: %w", err)
		}
		affected, _ := result.RowsAffected()
		if affected == 0 {
			return fmt.Errorf("outboxevents.MarkFailed pending: %w", ErrLeaseLost)
		}
	}
	return nil
}

// SupersedeStatus is the canonical outbox status for events
// obsoleted by a newer version of the same aggregate. Distinct from
// dead_letter so an operator dashboard distinguishes "the producer
// is broken" from "the upstream streamed a fresh update — old events
// became no-ops". Written by MarkSuperseded; recognised by the
// realtime.IndexHealth counters.
//
// Mirrors the terminal-success state introduced by the
// *SupersedeError path (see supersede.go). The status string is
// intentionally lower-cased + pluralised to map unambiguously onto
// the "completed | dead_letter | superseded" triad.
const SupersedeStatus = "superseded"

// MarkSuperseded moves a claimed event straight to status='superseded',
// bypassing the attempt-count+max-attempts comparison that
// MarkFailed applies on a retryable error. Use this when the
// handler returns a *SupersedeError — IsSupersede owns the classifier
// in Pool.processEvent.
//
// Lease-fenced — matches MarkCompleted / MarkDeadLetter. Mirrors the
// same worker_id / lease_id / lease_expiry reset to the empty/null
// set so a stale consumer cannot resurrect the row once retired.
// errMsg goes verbatim into last_error so operator log queries
// (last_error LIKE '%superseded%') surface this terminal state
// cleanly.
//
// Reference: QDRANT-002 checklist item F — "Se l'evento è obsoleto,
// marcarlo SUPERSEDED senza indicizzare dati vecchi."
func (r *Repository) MarkSuperseded(ctx context.Context, eventID int64, leaseID string, errMsg string) error {
	nowStr := timeutil.FormatRFC3339(time.Now())
	result, err := r.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = ?, last_error = ?, updated_at = ?,
		    worker_id = '', lease_id = '', lease_expiry = NULL
		WHERE id = ? AND lease_id = ? AND status = 'processing'
	`, SupersedeStatus, errMsg, nowStr, eventID, leaseID)
	if err != nil {
		return fmt.Errorf("outboxevents.MarkSuperseded(%d): %w", eventID, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("outboxevents.MarkSuperseded(%d): %w", eventID, ErrLeaseLost)
	}
	return nil
}

// MarkDeadLetter moves a claimed event straight to dead_letter,
// bypassing the attempt-count+max-attempts comparison in MarkFailed.
// Use this when the handler reports a terminal error (see errors.go
// for classification). Lease-fenced: the UPDATE matches only when the
// supplied lease_id is still active, returning ErrLeaseLost when
// another consumer has already taken over the event.
//
// Marks worker_id / lease_id / lease_expiry to the empty/null set
// (mirrors the in-line dead_letter branch of MarkFailed) so a stale
// consumer cannot resurrect the row once it has been retired.
//
// Guard style intentionally matches the neighbouring MarkFailed /
// MarkCompleted / RequeueExpiredLeases methods (no input-validation
// fences inside these methods — the Pool caller already proves
// the lease_id is the one it holds by being the sole claimer of
// the row in processEvent).
func (r *Repository) MarkDeadLetter(ctx context.Context, eventID int64, leaseID string, errMsg string) error {
	nowStr := timeutil.FormatRFC3339(time.Now())
	result, err := r.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = 'dead_letter', last_error = ?, updated_at = ?,
		    worker_id = '', lease_id = '', lease_expiry = NULL
		WHERE id = ? AND lease_id = ? AND status = 'processing'
	`, errMsg, nowStr, eventID, leaseID)
	if err != nil {
		return fmt.Errorf("outboxevents.MarkDeadLetter(%d): %w", eventID, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("outboxevents.MarkDeadLetter(%d): %w", eventID, ErrLeaseLost)
	}
	return nil
}

// RequeueExpiredLeases resets processing events with expired lease back to pending.
func (r *Repository) RequeueExpiredLeases(ctx context.Context) (int, error) {
	now := timeutil.FormatRFC3339(time.Now())
	result, err := r.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = 'pending', worker_id = '', lease_id = '', lease_expiry = NULL,
		    updated_at = ?
		WHERE status = 'processing'
		  AND lease_expiry IS NOT NULL
		  AND lease_expiry < ?
	`, now, now)
	if err != nil {
		return 0, fmt.Errorf("outboxevents.RequeueExpiredLeases: %w", err)
	}
	affected, _ := result.RowsAffected()
	return int(affected), nil
}
