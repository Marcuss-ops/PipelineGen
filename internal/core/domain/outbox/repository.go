package outbox

import (
	"context"
	"database/sql"
	"time"
)

// Repository is the canonical domain contract for the outbox_events table.
// Implementations live in internal/repository/outboxevents.
//
// The outbox ensures that data mutations and external side-effects are
// committed atomically: callers enqueue events inside a transaction, and
// a background worker pool claims and dispatches them with lease fencing.
//
// Services MUST depend on this interface, NOT on the concrete
// outboxevents.Repository. This enables test doubles and keeps the
// domain layer decoupled from SQLite.
type Repository interface {
	// Enqueue inserts a new outbox event. MUST be called inside an active
	// *sql.Tx. Uses ON CONFLICT(event_key) DO NOTHING for idempotency,
	// so duplicate ingestion of the same asset is safe.
	Enqueue(ctx context.Context, tx *sql.Tx, eventType, aggregateID, aggregateType, payloadJSON, eventKey string) error

	// ClaimNext atomically claims the oldest pending event using a CTE-based
	// atomic claim + lease fencing. Returns nil if no events are pending.
	// The returned Claim contains the worker_id and lease_id needed for
	// subsequent MarkCompleted / MarkFailed / RenewLease calls.
	ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration) (*Claim, error)

	// RenewLease extends the lease for a claimed event. Returns ErrLeaseLost
	// if the lease has already expired or the event was re-assigned.
	RenewLease(ctx context.Context, eventID int64, workerID, leaseID string, leaseTTL time.Duration) error

	// MarkCompleted marks an event as completed. Verifies status='processing'
	// AND lease_id to prevent stale consumers from completing re-assigned events.
	// Returns ErrLeaseLost on mismatch.
	MarkCompleted(ctx context.Context, eventID int64, leaseID string) error

	// MarkFailed handles a failed event. If attempts remain, goes back to
	// pending with exponential backoff. If exhausted, goes to dead_letter.
	// Verifies lease_id for fencing. Returns ErrLeaseLost on mismatch.
	MarkFailed(ctx context.Context, eventID int64, leaseID string, errMsg string, nextAttemptAt time.Time) error

	// CountByStatus returns the count of events in a given status bucket.
	// Used by realtime.IndexHealth for the pending_outbox and dead_letter counters.
	CountByStatus(ctx context.Context, status string) (int64, error)

	// RequeueExpiredLeases resets processing events with expired lease back to pending.
	// Returns the number of requeued events.
	RequeueExpiredLeases(ctx context.Context) (int, error)

	// ListPending returns all pending and processing events for dashboards.
	ListPending(ctx context.Context) ([]Event, error)
}
