// Package outboxevents provides the canonical outbox for external event dispatch.
// Every code path that mutates authoritative data AND triggers an external
// side-effect (Drive upload, webhook, notification, external indexing) MUST
// route through this repository's Enqueue method inside a transaction.
//
// Pattern:
//
//	BEGIN
//	UPDATE media_assets ...
//	INSERT INTO outbox_events (...) VALUES (...) ON CONFLICT(event_key) DO NOTHING
//	COMMIT
//
// A worker polls ClaimNext and dispatches to the appropriate handler.
// Events have four states: pending, processing, completed, dead_letter.
package outboxevents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ErrLeaseLost is returned by MarkCompleted and MarkFailed when the
// UPDATE matches zero rows — the caller's lease_id no longer matches the
// row in the database, meaning the event was either re-assigned after
// lease expiry, or has progressed to a terminal status.
var ErrLeaseLost = errors.New("outbox lease lost")

// Event represents a single outbox event row.
type Event struct {
	ID            int64
	EventType     string
	AggregateID   string
	AggregateType string
	PayloadJSON   string
	Status        string
	AttemptCount  int
	MaxAttempts   int
	LastError     string
	EventKey      string
	WorkerID      string
	LeaseID       string
	LeaseExpiry   *time.Time
	CompletedAt   *time.Time
	CreatedAt     string
	UpdatedAt     string
}

// Claim is the fencing token returned by ClaimNext.
type Claim struct {
	Event    Event
	WorkerID string
	LeaseID  string
}

// Repository wraps SQL access to the outbox_events table.
type Repository struct {
	db *sql.DB
}

// NewRepository creates a Repository backed by db.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Enqueue inserts a new outbox event. Call this inside a transaction.
// Uses ON CONFLICT(event_key) DO NOTHING for idempotency.
func (r *Repository) Enqueue(ctx context.Context, tx *sql.Tx, eventType, aggregateID, aggregateType, payloadJSON, eventKey string) error {
	now := timeutil.FormatRFC3339(time.Now())
	exec := r.exec(ctx, tx)
	_, err := exec(ctx,
		`INSERT INTO outbox_events (event_type, aggregate_id, aggregate_type, payload_json, event_key, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING`,
		eventType, aggregateID, aggregateType, payloadJSON, eventKey, now, now,
	)
	if err != nil {
		return fmt.Errorf("outboxevents.Enqueue(%s, %s): %w", eventType, aggregateID, err)
	}
	return nil
}

// ClaimNext claims the oldest pending event atomically using CTE.
func (r *Repository) ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration) (*Claim, error) {
	now := timeutil.FormatRFC3339(time.Now())
	leaseID := uuid.NewString()
	leaseExpiry := time.Now().Add(leaseTTL)
	leaseExpiryStr := timeutil.FormatRFC3339(leaseExpiry)

	// Atomic CTE claim: WITH candidate AS (...) UPDATE ... RETURNING id.
	// SQLite doesn't support RETURNING universally, so we use claim+refetch.
	result, err := r.db.ExecContext(ctx, `
		WITH candidate AS (
			SELECT id FROM outbox_events
			WHERE status = 'pending'
			  AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
			ORDER BY next_attempt_at ASC, id ASC
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
		       created_at, updated_at
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

// CountByStatus returns the count of outbox events in a given status bucket.
// Used by realtime.IndexHealth for the pending_outbox and dead_letter counters.
func (r *Repository) CountByStatus(ctx context.Context, status string) (int64, error) {
	if r.db == nil {
		return 0, fmt.Errorf("outboxevents.Repository: db is nil")
	}
	if status == "" {
		return 0, fmt.Errorf("outboxevents.CountByStatus: status is required")
	}
	var n int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM outbox_events WHERE status = ?", status,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("outboxevents.CountByStatus(%q): %w", status, err)
	}
	return n, nil
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

// ListPending returns all pending/processing events for dashboard.
func (r *Repository) ListPending(ctx context.Context) ([]Event, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, event_type, aggregate_id, aggregate_type, payload_json,
		       status, attempt_count, max_attempts, last_error,
		       event_key, worker_id, lease_id, lease_expiry, completed_at,
		       created_at, updated_at
		FROM outbox_events
		WHERE status IN ('pending', 'processing')
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("outboxevents.ListPending: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		evt, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("outboxevents.ListPending scan: %w", err)
		}
		events = append(events, *evt)
	}
	return events, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanEvent(s scanner) (*Event, error) {
	e := &Event{}
	var leaseExpiryStr, completedAtStr sql.NullString
	err := s.Scan(
		&e.ID, &e.EventType, &e.AggregateID, &e.AggregateType,
		&e.PayloadJSON, &e.Status, &e.AttemptCount, &e.MaxAttempts, &e.LastError,
		&e.EventKey, &e.WorkerID, &e.LeaseID, &leaseExpiryStr, &completedAtStr,
		&e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if leaseExpiryStr.Valid {
		t := timeutil.ParseRFC3339(leaseExpiryStr.String)
		if !t.IsZero() {
			e.LeaseExpiry = &t
		}
	}
	if completedAtStr.Valid {
		t := timeutil.ParseRFC3339(completedAtStr.String)
		if !t.IsZero() {
			e.CompletedAt = &t
		}
	}
	return e, nil
}

func (r *Repository) exec(ctx context.Context, tx *sql.Tx) func(context.Context, string, ...any) (sql.Result, error) {
	if tx != nil {
		return tx.ExecContext
	}
	return r.db.ExecContext
}
