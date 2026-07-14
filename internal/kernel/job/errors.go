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
