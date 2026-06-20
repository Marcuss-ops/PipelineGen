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
