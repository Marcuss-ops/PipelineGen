package job

import "errors"

// ErrDuplicate is the storage-neutral classification for an enqueue write that
// collided with an existing canonical idempotency/uniqueness key. Persistence
// adapters map driver-specific constraint errors to this sentinel.
var ErrDuplicate = errors.New("job: duplicate")

// ErrLeaseLost is returned when a worker-originated operation fails because
// the supplied lease_id no longer matches the job's current lease (the job
// has been reassigned to another worker) or the lease has expired.
var ErrLeaseLost = errors.New("jobs: lease lost — the job has been reassigned to another worker (fenced operation rejected)")

var ErrTransitionConflict = errors.New("jobs: transition conflict — current status/lease/revision differs from expected (CAS-fence mismatch)")
var ErrJobNotFound = errors.New("jobs: job not found (no row for the requested jobID)")
var ErrFinalizeAttemptOutcomeInvalid = errors.New("FinalizeAttempt: outcome not in canonical enum {Succeeded, FailedPermanent, ScheduleRetry} (fail-closed guard at fence)")
var ErrFinalizeAttemptResultMissing = errors.New("FinalizeAttempt: SUCCEEDED outcome requires non-empty Result (wire-consistency guard)")
var ErrFinalizeAttemptErrorMissing = errors.New("FinalizeAttempt: non-SUCCEEDED outcome requires non-empty ErrorMessage (silent-empty error trap)")
var ErrFinalizeAttemptArtifactStale = errors.New("FinalizeAttempt: artifact-state patch stale (row missing, job-id mismatch, or already-terminal state)")
var ErrFinalizeAttemptOutboxEventMissing = errors.New("FinalizeAttempt: outbox event missing required Type or EventKey (uniqueness invariant)")
var ErrFinalizeAttemptDLQIncompatible = errors.New("FinalizeAttempt: DLQPayload is only valid with FAILED_PERMANENT or SCHEDULE_RETRY outcomes (terminal-failure invariant)")

// ErrMissingDeps is the shared fail-closed sentinel for registration APIs
// invoked before the composition root has wired their dependencies.
var ErrMissingDeps = errors.New("job: required dependency is nil")
