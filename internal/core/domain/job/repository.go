package job

import (
	"context"
	"encoding/json"
	"fmt"
)

// Repository is the canonical domain contract for job persistence.
// Implementations live in the infrastructure layer.
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
	ClaimNext(ctx context.Context, workerID string, leaseTTLSeconds int, types []string) (*Job, error)

	// Complete marks a job as completed with a result.
	Complete(ctx context.Context, id string, result json.RawMessage) error

	// Fail marks a job as failed with an error message.
	Fail(ctx context.Context, id string, errMsg string) error

	// Cancel cancels a queued or running job.
	Cancel(ctx context.Context, id string) error

	// Retry re-enqueues a failed job for retry.
	Retry(ctx context.Context, id string) error
}

// ErrTransitionConflict is returned by Transition when the current status
// of the job does not match the expected from status (concurrent modification).
var ErrTransitionConflict = fmt.Errorf("job transition conflict: current status differs from expected")
