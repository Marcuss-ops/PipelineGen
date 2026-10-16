// Package repository defines the persistence contract for the central job
// queue. The in-memory store and the PostgreSQL backend both implement it, so
// the HTTP server and the lease-expiry loop never know which one is in use.
package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// ErrNotFound is returned by Get when the job does not exist.
var ErrNotFound = errors.New("job not found")

// ErrJobExists is returned by Submit/SubmitIdempotent when a job with the
// same ID is already present. It is the ONLY condition the HTTP server maps
// to 409 Conflict on submit: transient storage failures must stay
// distinguishable from duplicates so producers do not treat an outage as an
// idempotent success.
//
// It ALIASES client.ErrJobExists rather than declaring a second sentinel with
// the same message. Two values for one fact made errors.Is depend on which
// package's copy a layer happened to return: the server matches the repository
// sentinel while the producer client matches its own, so a future path that
// returned the other copy would stop being recognized as "already exists" and
// a replay would be reported as a failure (or, worse, an outage as a
// duplicate). The public wire contract owns the value; the repository re-exports
// it.
var ErrJobExists = client.ErrJobExists

// JobRepository is the storage contract for the central job queue.
//
// Every method takes the caller's context as its first parameter. Cancellation
// and tracing now reach the database driver: a SIGTERM, an aborted producer
// request or a cancelled claim long-poll stops the work in flight instead of
// leaving a statement running to completion behind a caller that has already
// given up.
//
// Propagating the context does NOT remove the implementation's own bound. A
// backend must still derive a per-operation deadline FROM the incoming context
// (the PostgreSQL backend does this in its opContext) so a stalled connection, a
// lock wait or a mid-failover server cannot pin the calling HTTP handler or
// worker goroutine forever: the caller's context may be long-lived, and "no
// deadline anywhere in the chain" remains an availability bug, not a style
// choice.
type JobRepository interface {
	// Submit enqueues a job. The ID is required and must be unique.
	Submit(ctx context.Context, job model.Job) error

	// Claim atomically claims the next pending job for a worker, returning the
	// job and its lease duration. It returns a nil job when the queue is empty.
	Claim(ctx context.Context, workerID string) (*model.Job, time.Duration, error)
	ClaimState(ctx context.Context, workerID string, state model.State) (*model.Job, time.Duration, error)

	// Get returns the current state of a job, including its artifact when
	// completed. It returns ErrNotFound when the job does not exist.
	Get(ctx context.Context, id string) (*model.Job, error)

	// Children returns child chunks ordered by chunk index.
	Children(ctx context.Context, parentJobID string) ([]*model.Job, error)

	// ByParent returns EVERY job submitted under one parent_job_id, in
	// submission (queued_at) order — the run-scoped read. It answers "what did
	// this run enqueue?" for an operator, which is a different question from
	// Children's assembly fan-in (child chunks in chunk order): a producer sets
	// parent_job_id to the master run id on every job it submits, including
	// standalone render jobs that are nobody's chunk. An unknown parent returns
	// an empty slice, never an error: "this run enqueued nothing" is a fact, not
	// a failure.
	ByParent(ctx context.Context, parentJobID string) ([]*model.Job, error)

	// ClaimFinalization atomically claims a parent whose children are ready.
	ClaimFinalization(ctx context.Context, parentJobID, workerID string) (*model.Job, bool, error)
	// RecoverableParents returns parent IDs that own children and are pending or finalizing after lease recovery.
	RecoverableParents(ctx context.Context) ([]string, error)

	// Complete marks a running job as completed and records the rendered
	// artifact. The service layer rejects incomplete artifact metadata before
	// this method is called.
	Complete(ctx context.Context, id, workerID string, artifact model.Artifact) error

	// Rendered marks a running job as rendered: its artifact is durably stored
	// but external publication failed, so the job stays out of `completed` and
	// is re-claimable for a publication-only retry.
	Rendered(ctx context.Context, id, workerID string, artifact model.Artifact, reason string) error

	// Fail marks a running job failed. Jobs that have not exhausted their
	// attempts are requeued; otherwise they are permanently failed.
	Fail(ctx context.Context, id, workerID, reason string) error

	// Renew extends the lease for a running job owned by workerID.
	Renew(ctx context.Context, id, workerID string) error

	// Retry resets a failed job back to pending state for re-execution.
	Retry(ctx context.Context, id string) error

	// Cancel moves a job that has not reached a terminal state to the
	// cancelled state. Cancelled is terminal: the job is never claimable again
	// and lease expiry never requeues it. Cancelling an already-cancelled job
	// is a no-op; cancelling a completed/failed job is an error.
	Cancel(ctx context.Context, id string) error

	// SetProgress stores the latest render progress reported by the worker
	// that owns the job's lease. It fails if the job is not running or the
	// reporting worker does not match the lease owner, so a stale worker
	// cannot overwrite the progress of the current owner.
	SetProgress(ctx context.Context, id, workerID string, p model.Progress) error

	// RequeueExpired requeues (or permanently fails) jobs whose lease elapsed,
	// returning the number of jobs affected.
	RequeueExpired(ctx context.Context, now time.Time) (int, error)

	// Stats returns a snapshot of the queue state.
	Stats(ctx context.Context) model.Stats
}

// IdempotencyRepository optionally provides atomic submit-or-return-existing
// semantics for callers retrying the same logical render request.
type IdempotencyRepository interface {
	SubmitIdempotent(ctx context.Context, job model.Job) (*model.Job, bool, error)
}

// BatchRepository is the atomic family-submit capability. A chunk producer
// must create the assembly anchor and all of its children in one transaction:
// submitting the anchor first leaves a race in which a render worker can claim
// it before the children exist and render the full plan a second time.
type BatchRepository interface {
	SubmitBatch(ctx context.Context, jobs []model.Job) error
}

// PermanentFailureRepository exposes an explicit terminal-failure transition
// for non-retryable job/configuration errors. Ordinary Fail retains the
// retry-attempt policy.
type PermanentFailureRepository interface {
	FailPermanently(ctx context.Context, id, workerID, reason string) error
}
