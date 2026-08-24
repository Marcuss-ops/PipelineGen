// Package operations — ports.go defines the canonical narrow
// port surface that the `GenerationSubmissionService` consumes.
//
// godlike/06 SSOT: this file is the SOLE canonical owner of
// the 4 typed-port interfaces the service depends on. The
// concrete SQLite adapters in
// `internal/infrastructure/database/sqlite/{jobs,outboxevents,operations}`
// already exist; `internal/app` constructs the typed adapters
// that wrap the concrete repositories and feed the service via
// the port surface declared here.
//
// godlike/07 minimum-blast-radius: every port is intentionally
// narrow (1-2 methods) so the service is testable in isolation
// with hand-rolled fakes, and so cross-package drift tests are
// not needed (each port is consumed by exactly one caller).
package operations

import (
	"context"
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/operations"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// JobEnqueuer is the canonical narrow port for inserting a job
// row inside a caller-owned transaction. The canonical concrete
// adapter is the `CreateInTx` method on
// `internal/infrastructure/database/sqlite/jobs.SQLiteStore`.
//
// godlike/07 NO-FAKE-AVAILABILITY: the port is intentionally
// NOT exposed as a "Enqueue" interface (which would carry the
// pre-FASE-2 `enqueueMu` mutex + UNIQUE-constraint rescue +
// fail-closed handler gate) — those are concerns of the
// canonical jobs.Service, not the submission service. The
// submission service is the SOLE caller of `CreateInTx` and
// the typed-error contract is propagated through the TX
// rollback (no handler gate, no correlation-id rescue, no
// mutex — the submission service's `submitMu` mutex is the
// single point of serialisation).
type JobEnqueuer interface {
	// CreateInTx inserts a new job row inside the caller's
	// transaction. The job's ID, Type, Status, Payload,
	// RetryCount, MaxRetries, ActiveKey, CorrelationID,
	// CreatedAt, UpdatedAt fields are all honoured verbatim.
	//
	// Returns the underlying driver error on failure. The
	// caller (submission service) treats any non-nil error
	// as a TX-rollback trigger.
	CreateInTx(ctx context.Context, tx *sql.Tx, j *job.Job) error
}

// OutboxEmitter is the canonical narrow port for emitting a
// single outbox event inside a caller-owned transaction. The
// canonical concrete adapter is
// `internal/infrastructure/database/sqlite/outboxevents.Repository`
// (the existing typed Repository satisfies the port natively
// — no adapter wrap is needed at the composition root).
//
// FASE 2 (July 2026): the FASE 2 event_type for script.generate
// is the canonical "script.generate.queued" string. The
// payload is a small JSON envelope with operation_id + job_id
// (the worker that drains the outbox reads the payload to
// route the event). event_key is the operation_id (UNIQUE on
// the outbox_events table → idempotent re-enqueue is a no-op).
type OutboxEmitter interface {
	// Enqueue inserts a new outbox event. The caller's tx
	// is honoured (atomic with the operations INSERT +
	// jobs INSERT in the same TX). event_key is UNIQUE
	// across the outbox_events table; a re-enqueue with the
	// same event_key is silently dropped (ON CONFLICT DO
	// NOTHING), and the existing row's ID is returned.
	Enqueue(
		ctx context.Context,
		tx *sql.Tx,
		eventType, aggregateID, aggregateType, payloadJSON, eventKey string,
	) (*outboxevents.EnqueueResult, error)
}

// OperationsRepository is the canonical port for the operations
// table (canonical owner: `internal/infrastructure/database/sqlite/operations`).
// The submission service consumes only the 3 methods needed
// for the Submit flow: lookup-prior, insert-new, supersede-prior.
//
// godlike/06 SSOT: this port IS the `operations.Repository`
// interface from the `internal/infrastructure/database/sqlite/operations`
// package — but redefined here as a narrow consumer-side view
// (the SQLite-side `Repository` has 4 methods; the service
// only needs 3). The composition root in `internal/app` adapts
// the concrete SQLite `Repository` to this narrow port via a
// thin wrapper that omits `UpdateState` (which is used only by
// the force_refresh path and exposed via the same wrapper
// since the service uses it directly).
type OperationsRepository interface {
	// Insert atomically writes a new operation row.
	Insert(ctx context.Context, op *operations.Operation, tx *sql.Tx) error
	// GetLatestForKey returns the most recent operation for
	// the (scope, idempotency_key) pair, or (nil, nil) when
	// no row matches.
	GetLatestForKey(ctx context.Context, scope operations.Scope, idempotencyKey string, tx *sql.Tx) (*operations.Operation, error)
	// UpdateState transitions the operation to newState.
	// Used by the force_refresh path to flip the prior
	// operation to SUPERSEDED in the same atomic TX as the
	// new operation's INSERT.
	UpdateState(ctx context.Context, id string, newState operations.State, tx *sql.Tx) error
}

// TxManager is the canonical narrow port for opening a
// caller-managed transaction. The canonical concrete adapter
// is the *sql.DB's BeginTx method. The port exists so the
// submission service can be tested with a fake TxManager
// (in-memory mock that wraps a *sql.DB).
//
// godlike/07 minimum-blast-radius: the port is intentionally
// narrow (just `BeginTx`) — a richer interface (e.g. with
// retry, savepoint support) would over-design the surface
// for the FASE 2 atomic-TX shape.
type TxManager interface {
	// BeginTx opens a new transaction. The returned *sql.Tx
	// is the single handle the submission service uses for
	// all 3 atomic operations (operations + jobs + outbox).
	BeginTx(ctx context.Context) (*sql.Tx, error)
}

// JobGetter is the canonical narrow port for reading a single
// Job row by ID. The submission service pulls the canonical
// live Job state on the idempotency-hit replay path AFTER
// reading the Operation row, so the HTTP layer can surface
// the canonical Job.Status on replay (instead of a cached
// pre-FASE-2 snapshot — which is the user-spec semantic:
// "leggendo lo stato del job canonico, non più una copia
// HTTP 202").
//
// godlike/07 minimum-blast-radius: the port is intentionally
// narrow (just Get) — the submission service does NOT need
// the full jobs.Service surface (Enqueue, List, Cancel,
// etc.). The canonical concrete adapter is the existing
// `*jobs.SQLiteStore.Get(ctx, id)` — the SQLite store
// satisfies this port natively; no adapter wrap is needed at
// the composition root.
//
// Read is OUTSIDE the caller's transaction (the port
// signature has no `tx` parameter): the canonical live Job
// state is what the caller wants on replay, NOT a TX-stale
// snapshot. Concurrent updates by workers are observed on
// the next read.
type JobGetter interface {
	Get(ctx context.Context, id string) (*job.Job, error)
}
