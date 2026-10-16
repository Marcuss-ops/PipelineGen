// events.go owns the canonical render_events event-type vocabulary.
//
// It lives in the public wire contract (not in the PostgreSQL repository) for
// the same reason the job states do: render_events is the durable "what
// happened" ledger every consumer reads — the queue server, the worker, the
// end-to-end verification scripts and the operational queries — and a private
// constant set in one storage backend could not be reused by any of them, so
// the vocabulary was re-typed as a literal in SQL, shell and documentation.
// The string values are the persisted contract; renaming one is a data
// migration, not a refactor.
package client

// Render-event types recorded in render_events. render_jobs.state describes how
// a job is NOW; these events reconstruct what actually happened.
const (
	// EventJobCreated is appended when a job is accepted (submit, including an
	// idempotent replay of an existing logical job).
	EventJobCreated = "JOB_CREATED"
	// EventJobClaimed is appended when a worker takes the job's lease.
	EventJobClaimed = "JOB_CLAIMED"
	// EventLeaseRenewed is appended when the owning worker extends its lease.
	EventLeaseRenewed = "LEASE_RENEWED"
	// EventJobCompleted is appended when the artifact is durable and the job
	// reaches its completed terminal state.
	EventJobCompleted = "JOB_COMPLETED"
	// EventJobFailed is appended when a job reaches its failed terminal state.
	EventJobFailed = "JOB_FAILED"
	// EventJobRequeued is appended when a lease expires or the worker reports a
	// retryable failure, returning the job to pending.
	EventJobRequeued = "JOB_REQUEUED"
	// EventJobRendered is appended when the render is durable but external
	// publication is still pending (the "rendered" state).
	EventJobRendered = "JOB_RENDERED"
	// EventJobCancelled is appended when a producer cancels a non-terminal job.
	EventJobCancelled = "JOB_CANCELLED"
)

// AllRenderEvents returns the canonical event-type vocabulary in lifecycle
// order. It is the list every carrier must agree with: the PostgreSQL ledger,
// the end-to-end verification scripts (pinned by
// queue/internal/repository/postgres/events_vocabulary_test.go) and the
// operational queries.
func AllRenderEvents() []string {
	return []string{
		EventJobCreated, EventJobClaimed, EventLeaseRenewed, EventJobRendered,
		EventJobCompleted, EventJobFailed, EventJobRequeued, EventJobCancelled,
	}
}
