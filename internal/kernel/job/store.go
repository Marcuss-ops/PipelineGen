// Package job — kernel canonical Store + JobBroker interfaces.
//
// Phase A.2 (June 2026): migrated from internal/domain/job/. The
// domain package re-exports as `type Store = kerneljob.Store` and
// `type JobBroker = kerneljob.JobBroker` (aliases, transparent).
//
// Per godlike/02 kernel rules: interface-signature references are
// intra-package (Status, Filter, Job, Event) or stdlib (context,
// encoding/json, time). Kernel does NOT import internal/domain/job/.
package job

import (
	"context"
	"encoding/json"
	"time"
)

// Store is the canonical persistence contract for jobs.
//
// fencing/CAS primitives only.
//
// Phase A.2 (June 2026): canonical home is internal/kernel/job/.
// Adapters (SQLiteStore etc.) continue to satisfy this interface via
// `var _ job.Store = (*SQLiteStore)(nil)`; the alias in
// internal/domain/job/ makes the assertion structurally identical
// to a pre-Phase-A.2 build.
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

	// FindByClientAndIdempotencyKey returns the most recent job matching
	// the (client_id, idempotency_key) pair regardless of status, or nil
	// if none. The canonical pre-INSERT dedup primitive for M2M
	// idempotency (PG-M2M, Aug 2026): a remote submitter that retries a
	// POST after a network drop gets the SAME job_id back instead of a
	// duplicate. Companion to the UNIQUE-constraint rescue on INSERT
	// (idx_jobs_client_idempotency, migration 251).
	FindByClientAndIdempotencyKey(ctx context.Context, clientID, idempotencyKey string) (*Job, error)

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
	// errMsg is the handler's dispatch error; it is persisted to both
	// jobs.error and job_events.data_json so operators can diagnose WHY
	// a job is retrying without reading server logs.
	ScheduleRetry(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, errMsg string, backoff time.Duration) error

	// Cancel cancels a queued or running job (operator action, no lease required).
	Cancel(ctx context.Context, id string) error

	// SetProgress updates the progress percentage and emits an event message.
	SetProgress(ctx context.Context, id string, progress int, message string) error

	// AddEvent records a human-readable event on the job timeline.
	AddEvent(ctx context.Context, id string, eventType, message string, data map[string]any) error

	// RenewLease extends the lease expiry for a running job owned by
	// workerID and atomically reports the post-renewal lease state
	// (Fase 4(b), July 2026). The returned RenewLeaseResult carries the
	// typed LeaseState so the worker can compose the lease-renewal path
	// with concurrent cancellation in a SINGLE SQL UPDATE — eliminating
	// the per-job 2-second IsCancelled-poll goroutine (the pre-Fase-4
	// startCancelWatcher at worker_execution.go).
	//
	// LeaseState semantics (godlike/06 SSOT, declared at
	// internal/kernel/job/lease_state.go):
	//   - LeaseStateContinue: lease extended; caller proceeds.
	//   - LeaseStateCancelRequested: jobs.cancelled_at IS NOT NULL; caller
	//     MUST abort the in-flight job (call jobCancel on the job ctx).
	//   - LeaseStateLeaseLost: no rows updated (lease stolen, expired,
	//     reaped); caller MUST treat the in-flight work as orphaned.
	//
	// The pre-Fase-4 callers that consumed only the error return value
	// (treating ErrLeaseLost as a generic lease-lost signal) MUST
	// migrate to inspect the typed result.State; the error return is
	// reserved for non-lease-state failures (network, SQL, etc).
	//
	// Pre-Fase-4 signature: RenewLease(ctx, id, workerID, leaseTTL) error
	// (Push 4.3 hard-break: returning RenewLeaseResult is the canonical
	// surface; no V2 method, no envelope).
	RenewLease(ctx context.Context, id string, workerID string, leaseTTL time.Duration) (RenewLeaseResult, error)

	// FinalizeAttempt is the canonical consolidated terminal-decision
	// primitive introduced by Fase 4(a) (July 2026). It collapses the
	// four sibling paths (Complete / Fail / ScheduleRetry — plus
	// optional DLQ archive + artifact_state patch + outbox event
	// emission) into ONE atomic SQLite transaction. The full typed
	// contract lives in FinalizeAttemptCommand (this package) +
	// FinalizeAttemptResult; godlike/06 SSOT discipline: this method
	// is the SINGLE canonical writer of terminal state transitions
	// out of {SUCCEEDED, FAILED, RETRY_WAIT}.
	//
	// Pre-Fase-4 callers used Complete/Fail/ScheduleRetry separately;
	// those methods remain on the Store interface (compat) and
	// delegates to FinalizeAttempt internally. The dedicated
	// Fail/DeadLetter/ScheduleRetry paths land via the lower-level
	// CompletedFromOutcome gateway in Push 4.6 (caller migration).
	// Additive introduction only.
	FinalizeAttempt(ctx context.Context, cmd FinalizeAttemptCommand) (FinalizeAttemptResult, error)

	// DeadLetter archives a job that exhausted retries into the dead_letter_jobs table.
	DeadLetter(ctx context.Context, id string, errMsg string) error
}

// JobBroker is the canonical port under which any persistence
// implementation declares conformance (compile-time assertion in
// the adapter's package: `var _ job.JobBroker = (*Adapter)(nil)`).
//
// Phase A.2 (June 2026): canonical home is internal/kernel/job/.
//
// Today the in-tree adapter is *SQLiteStore
// (internal/platform/sqlite/jobs/repository.go).
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
