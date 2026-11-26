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
// Events have five states:
//   - pending     — eligible for ClaimNext
//   - processing  — claimed by a worker (lease_id non-empty)
//   - completed   — terminal success
//   - dead_letter — terminal failure (terminal error or max_attempts)
//   - superseded  — terminal "skipped" (event obsoleted by a newer
//     aggregate version; routed by *SupersedeError —
//     see outboxevents/supersede.go)
//
// The status column accepts any TEXT (no CHECK constraint on the
// canonical lifecycle set); writes to completed / dead_letter /
// superseded MUST go through MarkCompleted / MarkDeadLetter /
// MarkSuperseded respectively to keep lease fencing intact.
//
// File layout (godlike/06 SSOT one-canonical-owner-per-fact):
//   - repository.go         — this file (types + ctor + helpers)
//   - repository_write.go   — write/lifecycle methods (Enqueue + ClaimNext + RenewLease + MarkCompleted + MarkFailed + MarkDeadLetter + MarkSuperseded + RequeueExpiredLeases + SupersedeStatus const)
//   - repository_query.go   — read-only dashboard queries (CountByStatus + CountByEventTypeAndStatus + ListPending)
package outboxevents

import (
	"context"
	"database/sql"

	"errors"

	"github.com/Marcuss-ops/PipelineGen/pkg/sqlport"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/outbox"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ErrLeaseLost is returned by MarkCompleted and MarkFailed when the
// UPDATE matches zero rows — the caller's lease_id no longer matches the
// row in the database, meaning the event was either re-assigned after
// lease expiry, or has progressed to a terminal status.
var ErrLeaseLost = errors.New("outbox lease lost")

// Event represents a single outbox event row. It is the engine-independent
// DTO owned by internal/kernel/outbox; this alias keeps the adapter's public
// surface stable.
type Event = outbox.Event

// Priority constants for outbox event scheduling (migration 186). OWNED by
// internal/kernel/event/outbox_status.go (godlike/06 one owner per fact);
// this is a compile-time re-export so the SQLite and PostgreSQL engines cannot
// drift. ClaimNext orders by (priority DESC, next_attempt_at ASC, id ASC).
const (
	PriorityNormal = event.OutboxPriorityNormal
	PriorityHigh   = event.OutboxPriorityHigh
)

// Claim is the fencing token returned by ClaimNext.
type Claim struct {
	Event    Event
	WorkerID string
	LeaseID  string
}

// EnqueueResult is the typed feedback from Enqueue, owned by
// internal/kernel/outbox so producers can consume it without importing this
// adapter.
type EnqueueResult = outbox.EnqueueResult

// Repository wraps SQL access to the outbox_events table.
type Repository struct {
	db sqlport.ExecQueryer
}

// NewRepository creates a Repository backed by db.
func NewRepository(db sqlport.ExecQueryer) *Repository {
	return &Repository{db: db}
}

// scanner is the minimum interface satisfied by *sql.Row and *sql.Rows,
// enabling scanEvent to be reused for both single-row and multi-row scans.
type scanner interface {
	Scan(dest ...any) error
}

// scanEvent unmarshals one outbox_events row into an Event struct. The
// lease_expiry + completed_at columns are nullable TIMESTAMP — the
// driver's NullString is the lowest-common-denominator probe.
func scanEvent(s scanner) (*Event, error) {
	e := &Event{}
	var leaseExpiryStr, completedAtStr sql.NullString
	err := s.Scan(
		&e.ID, &e.EventType, &e.AggregateID, &e.AggregateType,
		&e.PayloadJSON, &e.Status, &e.AttemptCount, &e.MaxAttempts, &e.LastError,
		&e.EventKey, &e.WorkerID, &e.LeaseID, &leaseExpiryStr, &completedAtStr,
		&e.CreatedAt, &e.UpdatedAt, &e.Priority,
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

// exec returns the right exec-handle based on whether a tx is in scope.
// Callers passing a tx get transactional writes (the canonical pattern for
// outbox writes that must be atomic with their producer-state mutation);
// callers passing nil get a direct db.ExecContext (rare; used by
// non-tx-bound diagnostic paths).
func (r *Repository) exec(ctx context.Context, tx sqlport.Tx) func(context.Context, string, ...any) (sql.Result, error) {
	if tx != nil {
		return tx.ExecContext
	}
	return r.db.ExecContext
}
