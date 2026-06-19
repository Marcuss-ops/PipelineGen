package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// Store is the canonical domain contract for job persistence.
type Store interface {
	// Create inserts a new job in queued state.
	Create(ctx context.Context, j *job.Job) error

	// Get returns a job by ID, or nil if not found.
	Get(ctx context.Context, id string) (*job.Job, error)

	// List returns jobs matching the given filter.
	List(ctx context.Context, filter job.Filter) ([]job.Job, error)

	// ClaimNext claims the oldest queued job for the given worker,
	// setting status=LEASED and the lease expiry.
	ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration, types []string) (*job.Job, error)

	// Complete marks a job as completed with a result. Fenced by lease.
	Complete(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, result json.RawMessage) error

	// Fail marks a job as failed with an error message. Fenced by lease.
	Fail(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, errMsg string) error

	// ScheduleRetry re-enqueues a running job for retry with backoff.
	// Fenced by lease. Used when the handler returns a retryable error.
	ScheduleRetry(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, backoff time.Duration) error

	// Cancel cancels a queued or running job (operator action, no lease required).
	Cancel(ctx context.Context, id string) error

	// SetProgress updates the progress percentage and optionally records an event.
	SetProgress(ctx context.Context, id string, progress int, message string) error

	// AddEvent records a human-readable event on the job timeline.
	AddEvent(ctx context.Context, id string, eventType, message string, data map[string]any) error

	// RenewLease extends the lease expiry for a running job owned by workerID.
	RenewLease(ctx context.Context, id string, workerID string, leaseTTL time.Duration) error

	// DeadLetter archives a job that exhausted retries into the dead_letter_jobs table.
	DeadLetter(ctx context.Context, id string, errMsg string) error
}

// ErrLeaseLost is returned by worker-originated operations when the supplied lease_id
// no longer matches the job's current lease.
var ErrLeaseLost = fmt.Errorf("lease lost: the job has been reassigned to another worker")

// ErrTransitionConflict is returned when the current status of the job does
// not match the expected status (concurrent modification).
var ErrTransitionConflict = fmt.Errorf("job transition conflict: current status differs from expected")
