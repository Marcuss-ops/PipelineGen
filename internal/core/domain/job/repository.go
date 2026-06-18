package job

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Repository is the canonical domain contract for job persistence.
// Implementations live in the infrastructure layer.
//
// All worker-originated operations (Complete, Fail, ScheduleRetry, ConfirmCancelled)
// REQUIRE job_id, worker_id, lease_id, and expected_revision so the repository can
// fence stale operations: a completion with an expired lease returns ErrLeaseLost.
type Repository interface {
	// Create inserts a new job in queued state.
	Create(ctx context.Context, j *Job) error

	// Get returns a job by ID, or nil if not found.
	Get(ctx context.Context, id string) (*Job, error)

	// List returns jobs matching the given filter.
	List(ctx context.Context, filter Filter) ([]Job, error)

	// Transition atomically transitions a job from one status to another.
	// The UPDATE uses a WHERE status = from guard so concurrent updates
	// cannot bypass the state machine. Returns ErrTransitionConflict if
	// the current status does not match the expected from status.
	//
	// This is the SINGLE entry point for all job state transitions.
	// Complete, Fail, Cancel, and Retry all route through Transition.
	Transition(ctx context.Context, id string, from, to Status) error

	// ClaimNext claims the oldest queued job for the given worker,
	// setting status=running and the lease expiry.
	ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration, types []string) (*Job, error)

	// ── Worker-originated operations (lease-fenced) ───────────────────
	// Every operation below carries worker_id + lease_id + expected_revision.
	// The repository MUST validate that the lease is still owned by the caller
	// before executing the transition. Stale-lease operations return ErrLeaseLost.

	// Complete marks a job as completed with a result. Fenced by lease.
	Complete(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, result json.RawMessage) error

	// Fail marks a job as failed with an error message. Fenced by lease.
	Fail(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, errMsg string) error

	// ScheduleRetry re-enqueues a running job for retry with backoff.
	// Fenced by lease. Used when the handler returns a retryable error.
	ScheduleRetry(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, backoff time.Duration) error

	// Retry re-enqueues a failed job for retry (operator action, no lease required).
	Retry(ctx context.Context, id string) error

	// Cancel cancels a queued or running job.
	Cancel(ctx context.Context, id string) error

	// ── Progress + events (not lease-fenced — monotonic writes) ───────

	// SetProgress updates the progress percentage and optionally records an event.
	SetProgress(ctx context.Context, id string, progress int, message string) error

	// AddEvent records a human-readable event on the job timeline.
	AddEvent(ctx context.Context, id string, eventType, message string, data map[string]any) error

	// RenewLease extends the lease expiry for a running job owned by workerID.
	RenewLease(ctx context.Context, id string, workerID string, leaseTTL time.Duration) error

	// DeadLetter archives a job that exhausted retries into the dead_letter_jobs table.
	DeadLetter(ctx context.Context, id string, errMsg string) error
}

// ErrTransitionConflict is returned by Transition when the current status
// of the job does not match the expected from status (concurrent modification).
var ErrTransitionConflict = fmt.Errorf("job transition conflict: current status differs from expected")

// ErrLeaseLost is returned by worker-originated operations (Complete, Fail,
// ScheduleRetry) when the supplied lease_id no longer matches the job's current
// lease — meaning another worker has claimed this job, or the lease expired
// and a stale operation is trying to finalise a re-assigned job.
var ErrLeaseLost = fmt.Errorf("lease lost: the job has been reassigned to another worker")
