package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// ── Errors ──────────────────────────────────────────────────────────────

var (
	ErrAlreadyClaimed = errors.New("job already claimed by another worker")
	ErrJobNotFound    = errors.New("job not found")
	ErrInvalidState   = errors.New("invalid state transition")
	// ErrTransitionConflict and ErrLeaseLost live in store.go (Wave 17.1.2
	// canonical home) — they are package-scope vars any file in
	// `jobs` can reference without re-declaring or aliasing.
)

// ── Typed Command Structs ────────────────────────────────────────────────

// StartJob transitions a queued or leased job to running. Used by
// ClaimNext internally and by tests for direct transition assertions.
//
// The LeaseTTL is mandatory: the SQL update writes the lease_expiry
// column to now + cmd.LeaseTTL, so a zero value would expire the lease
// instantly. Callers constructed from ClaimNext always carry the
// runner's leaseTTL; manual callers must use real values.
//
// Order in the struct matches the SQL UPDATE column order
// (started_at, lease_expiry, lease_id, worker_id, updated_at) so the
// field-name ordering is easy to correlate with the codebase.
type StartJob struct {
	JobID    string
	WorkerID string
	LeaseID  string
	// LeaseTTL is required (non-zero). Zero would expire the lease
	// immediately on transition.
	LeaseTTL time.Duration
	// Revision is the CAS revision read at SELECT-time. The UPDATE
	// is gated by `AND revision = ?` so a stale Revision produces
	// rows-affected=0 → ErrTransitionConflict.
	Revision int64
}

// RenewLease extends an active lease.
type RenewLease struct {
	JobID         string
	WorkerID      string
	LeaseID       string
	Revision      int64
	NewExpiration time.Time
}

// CompleteJob marks a job as completed.
type CompleteJob struct {
	JobID      string
	WorkerID   string
	LeaseID    string
	Revision   int64
	ResultJSON json.RawMessage
}

// FailJob marks a job as failed.
type FailJob struct {
	JobID    string
	WorkerID string
	LeaseID  string
	Revision int64
	Error    string
}

// ScheduleRetry transitions a running job to retry_wait (or failed if no retries remain).
type ScheduleRetry struct {
	JobID    string
	WorkerID string
	LeaseID  string
	Revision int64
}

// RequestCancel transitions a non-terminal job to cancelled from any active state.
type RequestCancel struct {
	JobID string
}

// ConfirmCancelled is called after a worker acknowledges a cancel request.
type ConfirmCancelled struct {
	JobID    string
	WorkerID string
	LeaseID  string
	Revision int64
}

// ── Lease and Result Types ───────────────────────────────────────────────

// RequeueResult is returned by RequeueExpiredLeases for each expired lease.
type RequeueResult struct {
	JobID     string
	NewStatus job.Status
	Error     string
}

// ── Transition Validation ────────────────────────────────────────────────

// ValidateTransition checks if the state transition is allowed per the
// canonical 7-state machine.
func ValidateTransition(current, next job.Status) error {
	switch current {
	case job.StatusQueued:
		switch next {
		case job.StatusLeased, job.StatusCancelled:
			return nil
		}
	case job.StatusLeased:
		switch next {
		case job.StatusRunning, job.StatusQueued, job.StatusCancelled:
			return nil
		}
	case job.StatusRunning:
		switch next {
		case job.StatusFinalizing, job.StatusSucceeded, job.StatusRetryWait, job.StatusFailed, job.StatusCancelled:
			return nil
		}
	case job.StatusFinalizing:
		switch next {
		case job.StatusSucceeded, job.StatusRetryWait, job.StatusFailed, job.StatusCancelled:
			return nil
		}
	case job.StatusRetryWait:
		switch next {
		case job.StatusQueued, job.StatusFailed, job.StatusCancelled:
			return nil
		}
	case job.StatusSucceeded, job.StatusFailed, job.StatusCancelled:
		return fmt.Errorf("cannot transition from terminal status %q to %q", current, next)
	}
	return fmt.Errorf("invalid transition: %q → %q", current, next)
}
