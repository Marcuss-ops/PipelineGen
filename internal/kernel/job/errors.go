package job

import "errors"

// ErrLeaseLost is returned when a worker-originated operation fails because
// the supplied lease_id no longer matches the job's current lease (the job
// has been reassigned to another worker) or the lease has expired.
var ErrLeaseLost = errors.New("jobs: lease lost — the job has been reassigned to another worker (fenced operation rejected)")

// ErrTransitionConflict is returned when a state transition is attempted
// against a row whose current state does not match the expected pre-state
// (concurrent modification detected via the CAS-fence on revision).
var ErrTransitionConflict = errors.New("jobs: transition conflict — current status/lease/revision differs from expected (CAS-fence mismatch)")

// ErrJobNotFound is returned when the queried jobID does not exist in the
// jobs table.
var ErrJobNotFound = errors.New("jobs: job not found (no row for the requested jobID)")

var ErrFinalizeAttemptOutcomeInvalid = errors.New("FinalizeAttempt: outcome not in canonical enum {Succeeded, FailedPermanent, ScheduleRetry} (fail-closed guard at fence)")
var ErrFinalizeAttemptResultMissing = errors.New("FinalizeAttempt: SUCCEEDED outcome requires non-empty Result (wire-consistency guard)")
var ErrFinalizeAttemptErrorMissing = errors.New("FinalizeAttempt: non-SUCCEEDED outcome requires non-empty ErrorMessage (silent-empty error trap)")
var ErrFinalizeAttemptArtifactStale = errors.New("FinalizeAttempt: artifact-state patch stale (row missing, job-id mismatch, or already-terminal state)")
var ErrFinalizeAttemptOutboxEventMissing = errors.New("FinalizeAttempt: outbox event missing required Type or EventKey (uniqueness invariant)")
var ErrFinalizeAttemptDLQIncompatible = errors.New("FinalizeAttempt: DLQPayload is only valid with FAILED_PERMANENT or SCHEDULE_RETRY outcomes (terminal-failure invariant)")
