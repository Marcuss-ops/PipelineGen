// Package job defines the canonical domain types and persistence contract
// for the job system.
//
// These types are the single source of truth (SSOT) for the job entity,
// status, filter, and persistence interface. Implementations live under
// infrastructure/database/sqlite/jobs/.
//
// Store is the canonical persistence contract. The rich signature (with
// concurrency fencing inline) was promoted from the SQLite implementation
// in Onda 5 PR 1 — see CHANGELOG for migration path.
package job

import (
	"context"
	"encoding/json"
	"time"
)

// Store is the canonical persistence contract for jobs.
//
// All state-changing operations accept the lease fencing tuple
// (workerID, leaseID, expectedRevision) inline. Implementations MUST
// perform an optimistic-concurrency check before mutating job state.
type Store interface {
	// Create inserts a new job in queued state.
	Create(ctx context.Context, j *Job) error

	// Get returns a job by ID, or nil if not found.
	Get(ctx context.Context, id string) (*Job, error)

	// List returns jobs matching the given filter.
	List(ctx context.Context, filter Filter) ([]Job, error)

	// FindActiveByKey returns the most recent job matching the given
	// active_key with a non-terminal status, or nil if none. The
	// canonical pre-INSERT dedup primitive for active-key idempotency.
	FindActiveByKey(ctx context.Context, activeKey string) (*Job, error)

	// FindByTypeAndCorrelation returns the most recent job matching the
	// (type, correlation_id) pair regardless of status, or nil if none.
	// The canonical pre-INSERT dedup primitive for correlation-id
	// idempotency (companion to UNIQUE-constraint rescue on INSERT).
	FindByTypeAndCorrelation(ctx context.Context, jobType string, correlationID string) (*Job, error)

	// ListEvents returns all events on a job's timeline in created_at
	// ascending order. Used by the timeline view in the operator UI.
	ListEvents(ctx context.Context, jobID string) ([]Event, error)

	// Retry re-enqueues a job whose current status is RETRY_WAIT or
	// FAILED — used for operator-triggered manual retries or for the
	// (type, correlation_id) post-completion resubmit path.
	Retry(ctx context.Context, id string) (*Job, error)

	// ClaimNext claims the oldest queued job for the given worker,
	// setting status=LEASED and the lease expiry.
	ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration, types []string) (*Job, error)

	// Complete marks a job as completed with a result. Fenced by lease.
	Complete(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, result json.RawMessage) error

	// Fail marks a job as failed with an error message. Fenced by lease.
	Fail(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, errMsg string) error

	// ScheduleRetry re-enqueues a running job for retry with backoff.
	// Fenced by lease. Used when the handler returns a retryable error.
	ScheduleRetry(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, backoff time.Duration) error

	// Cancel cancels a queued or running job (operator action, no lease required).
	Cancel(ctx context.Context, id string) error

	// SetProgress updates the progress percentage and emits an event message.
	SetProgress(ctx context.Context, id string, progress int, message string) error

	// AddEvent records a human-readable event on the job timeline.
	AddEvent(ctx context.Context, id string, eventType, message string, data map[string]any) error

	// RenewLease extends the lease expiry for a running job owned by workerID.
	RenewLease(ctx context.Context, id string, workerID string, leaseTTL time.Duration) error

	// DeadLetter archives a job that exhausted retries into the dead_letter_jobs table.
	DeadLetter(ctx context.Context, id string, errMsg string) error
}

// JobBroker is the canonical port under which any persistence
// implementation declares conformance (compile-time assertion in
// the adapter's package: `var _ job.JobBroker = (*Adapter)(nil)`).
//
// Today the in-tree adapter is *SQLiteStore
// (internal/infrastructure/database/sqlite/jobs/repository.go).
// A future PostgreSQL adapter (post-godlike/06 multi-node) declares
// the same assertion in its own repository file; the assertion + the
// shared Store interface are the seam that lets the application
// layer depend on a portable port instead of the concrete implementation.
//
// Shape (B) — PR-B, Wave 22, June 2026: embed Store so the port's
// total surface equals Store's surface today. Future broker-specific
// primitives (e.g. a cross-node reservation API) extend this interface
// here without modifying the canonical Store contract; adapters that
// cannot implement them stay out of the port (per godlike/07 "no
// fake availability"). Embedding over a type alias (`type JobBroker
// = Store`) is the cheaper-than-alias surface convention: a future
// type-alias would force consumers to spell the alias against every
// port reference, and a struct-equality test (`_ = JobBroker(nil)`)
// would lose the ability to extend.
//
// Why embedding-not-alias (rationale): see ADR-0002 §D2
// (`architecture/decisions/0002-p2-p3-roadmap.md`). A future PR-postgres
// that proposes collapsing JobBroker to `type JobBroker = Store` MUST
// first re-ratify §D2 — otherwise the rationale is silently lost.
type JobBroker interface {
	Store
}
