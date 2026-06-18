// Package jobs provides the canonical job repository with atomic CAS operations
// for the 7-state job lifecycle (PR-2: atomic lifecycle).
//
// States: PENDING → LEASED → RUNNING → SUCCEEDED / RETRY_WAIT / FAILED / CANCELLED
package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
)

// ── Errors ──────────────────────────────────────────────────────────────

var (
	ErrTransitionConflict  = errors.New("transition conflict: row was modified by another worker")
	ErrLeaseLost           = errors.New("lease lost: worker_id or lease_id mismatch")
	ErrAlreadyClaimed      = errors.New("job already claimed by another worker")
	ErrJobNotFound         = errors.New("job not found")
	ErrInvalidState        = errors.New("invalid state transition")
)

// ── Typed Command Structs ────────────────────────────────────────────────

// CreateJob is the input for Repository.Create.
type CreateJob struct {
	Job           *models.Job
}

// ClaimNext is the input for Repository.ClaimNext.
type ClaimNext struct {
	WorkerID string
	LeaseID  string
	LeaseTTL time.Duration
	Types    []string
}

// StartJob is the input for Repository.Start.
// Transitions PENDING or LEASED → RUNNING under the caller's lease.
type StartJob struct {
	JobID      string
	WorkerID   string
	LeaseID    string
	LeaseTTL   time.Duration
	Revision   int64
}

// RenewLease extends an active lease.
type RenewLease struct {
	JobID         string
	WorkerID      string
	LeaseID       string
	Revision      int64
	NewExpiration time.Time
}

// CompleteJob marks a job as SUCCEEDED.
type CompleteJob struct {
	JobID      string
	WorkerID   string
	LeaseID    string
	Revision   int64
	ResultJSON json.RawMessage
}

// FailJob marks a job as FAILED.
type FailJob struct {
	JobID    string
	WorkerID string
	LeaseID  string
	Revision int64
	Error    string
}

// ScheduleRetry transitions a RUNNING job to RETRY_WAIT (or FAILED if no retries remain).
type ScheduleRetry struct {
	JobID    string
	WorkerID string
	LeaseID  string
	Revision int64
}

// RequestCancel transitions a non-terminal job to CANCELLED from any active state.
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

// Lease is returned by ClaimNext on success.
type Lease struct {
	Job         *models.Job
	LeaseID     string
	LeaseExpiry time.Time
}

// RequeueResult is returned by RequeueExpiredLeases for each expired lease.
type RequeueResult struct {
	JobID    string
	NewStatus models.JobStatus
	Error    string
}

// ── Transition Validation ────────────────────────────────────────────────

// NOTE: Repository struct is declared in repository.go.
// The typed command structs above are the canonical API surface for all
// lifecycle operations on *Repository.
//

// ValidateTransition checks if the state transition is allowed per the
// canonical 7-state machine.
func ValidateTransition(current, next models.JobStatus) error {
	switch current {
	case models.StatusPending:
		switch next {
		case models.StatusLeased, models.StatusCancelled:
			return nil
		}
	case models.StatusLeased:
		switch next {
		case models.StatusRunning, models.StatusPending, models.StatusCancelled:
			return nil
		}
	case models.StatusRunning:
		switch next {
		case models.StatusSucceeded, models.StatusRetryWait, models.StatusFailed, models.StatusCancelled:
			return nil
		}
	case models.StatusRetryWait:
		switch next {
		case models.StatusPending, models.StatusFailed, models.StatusCancelled:
			return nil
		}
	case models.StatusSucceeded, models.StatusFailed, models.StatusCancelled:
		return fmt.Errorf("cannot transition from terminal status %q to %q", current, next)
	}
	return fmt.Errorf("invalid transition: %q → %q", current, next)
}
