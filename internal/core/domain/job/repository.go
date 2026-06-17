// Package job is the canonical domain layer for jobs in VeloxEditing.
//
// The legacy code mixes persistence types (models.Job) directly with
// repository calls, which couples the worker runtime to the SQLite
// driver. This package introduces a domain-level interface and a
// centralised Transition primitive guarded by an optimistic-locking
// `revision` column. Both SQLite (legacy) and a future Postgres
// implementation will satisfy JobRepository.
package job

import (
	"context"
	"time"

	"velox/go-master/internal/media/models"
)

// Job is the domain-side alias for the persisted job struct. We
// re-export models.Job as a type alias (not a wrapper) so existing
// call sites keep compiling while the domain surface stabilises.
// All persistence concerns (driver, schema) stay in internal/repository/jobs.
type Job = models.Job

// Status mirrors the 5-state canonical set on models.JobStatus.
// We re-export the constants here so callers can depend on the
// domain package without importing the persistence-side models.
type Status = models.JobStatus

const (
	StatusQueued    = models.StatusQueued
	StatusRunning   = models.StatusRunning
	StatusCompleted = models.StatusCompleted
	StatusFailed    = models.StatusFailed
	StatusCancelled = models.StatusCancelled
)

// JobType aliases the persisted JobType so the domain layer stays
// free of imports from the persistence adapter side once we move
// fully to internal/core/domain/{asset,workflow,delivery}.
type JobType = models.JobType

// ClaimRequest captures the parameters a worker pool uses to claim
// the next available job. Future PRs can extend this with
// capability filters without breaking the JobRepository interface.
type ClaimRequest struct {
	WorkerID string
	LeaseTTL time.Duration
	JobTypes []models.JobType
}

// TransitionRequest is the canonical interface for advancing a job's
// state. Every status-changing write in the system flows through a
// Transition, guarded by an expected revision + status (optimistic
// lock + lost-update guard).
//
// Updates is a free-form map of additional column → value that the
// repository applies atomically with the status change. Allowed keys
// depend on the underlying schema; the canonical set today is:
//   - result_json (string|[]byte)
//   - progress (int)
//   - error (string)
//   - completed_at (*time.Time)
//   - cancelled_at (*time.Time)
//   - started_at (*time.Time)
//   - lease_expiry (*time.Time)
//   - worker_id (string)
//   - retry_count (int)
//   - active_key (string)
type TransitionRequest struct {
	JobID            string
	ExpectedRevision int
	ExpectedStatus   models.JobStatus
	NewStatus        models.JobStatus
	Updates          map[string]any
}

// JobRepository is the domain-level interface for job persistence.
// The concrete implementation lives in internal/repository/jobs and
// can be swapped for a Postgres-backed Repository without touching
// callers.
//
// Every method that mutates status flows through Transition so the
// optimistic-lock contract is enforced uniformly.
type JobRepository interface {
	// Create enqueues a new job in StatusQueued with revision=1.
	Create(ctx context.Context, job *Job) error

	// Get returns the current job state, or nil if the id is unknown.
	Get(ctx context.Context, id string) (*Job, error)

	// ClaimNext picks the next eligible job (status=queued, lease expired)
	// and atomically transitions it to running under the supplied lease.
	ClaimNext(ctx context.Context, req ClaimRequest) (*Job, error)

	// Transition advances a job from ExpectedStatus to NewStatus, validating
	// the optimistic-lock token. Returns the refreshed job (with bumped
	// revision) on success.
	Transition(ctx context.Context, req TransitionRequest) (*Job, error)

	// RenewLease extends the lease for the in-flight job, keyed by the
	// worker identity so two workers can't accidentally grant each
	// other's leases. (leaseID will be added in PR-1.5 alongside the
	// worker registry; for now the worker identity is sufficient.)
	RenewLease(ctx context.Context, jobID, workerID string, ttl time.Duration) error
}
