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
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
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
	// Priority is the scheduling priority stamped by the producer
	// (migration 186). ClaimNext claims higher values first; default 5.
	Priority int
}

// Priority constants for outbox event scheduling (migration 186).
// ClaimNext orders by (priority DESC, next_attempt_at ASC, id ASC).
//
//   - PriorityNormal (5)  : bulk-folder-sync / catalog re-sync — the
//     default for producers that do not stamp an explicit priority.
//   - PriorityHigh (10)   : script-required index requests (stock
//     pipeline finalizer emitting asset.index.requested for assets a
//     script generation job is blocked on).
const (
	PriorityNormal = 5
	PriorityHigh   = 10
)

// Claim is the fencing token returned by ClaimNext.
type Claim struct {
	Event    Event
	WorkerID string
	LeaseID  string
}

// EnqueueResult is the typed feedback from Enqueue. Before Blocco 2.0,
// ON CONFLICT(event_key) DO NOTHING silently suppressed duplicate
// inserts with zero feedback — the producer had no way to know
// whether the event was freshly inserted or silently ignored
// (potentially because a completed/dead_letter/superseded row
// already existed with the same event_key).
//
// Inserted=true means the INSERT landed (no conflict existed).
// Inserted=false means ON CONFLICT fired; ExistingStatus carries
// the existing row's status so the producer can decide whether
// to retry with a new event_key, surface a warning, or move on.
type EnqueueResult struct {
	EventID               int64  // ID of the row (new or existing)
	Inserted              bool   // true if INSERT landed, false if ON CONFLICT suppressed
	ExistingStatus        string // existing row's status when Inserted=false; empty when Inserted=true
	ExistingEventType     string // existing event type when Inserted=false
	ExistingAggregateType string // existing aggregate type when Inserted=false
	ExistingAggregateID   string // existing aggregate ID when Inserted=false
}

// Repository wraps SQL access to the outbox_events table.
type Repository struct {
	db *sql.DB
}

// NewRepository creates a Repository backed by db.
func NewRepository(db *sql.DB) *Repository {
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
func (r *Repository) exec(ctx context.Context, tx *sql.Tx) func(context.Context, string, ...any) (sql.Result, error) {
	if tx != nil {
		return tx.ExecContext
	}
	return r.db.ExecContext
}
