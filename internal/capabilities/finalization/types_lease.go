// types/types_lease.go — one canonical type per godlike/06 SSOT.
// Code-motion split from internal/capabilities/finalization/types.go (674 LOC, LONG-FILES-DECOMPOSITION-2026-07-06 P0 critical band slice, 2026-07-06).
package finalization

import "time"

// Lease represents a claimed job lease. The finalizer validates that the
// lease is still valid (not expired) and belongs to the calling worker
// before committing the completion transaction.
//
// Mapping note (July 2026): jobs.Lease in internal/capabilities/jobs/queue/
// carries a *job.Job pointer; this domain Lease carries a flat JobID
// string instead, avoiding coupling the domain layer to the infrastructure
// Job struct. FASE 2's JobFinalizer adapter will map between the two.
type Lease struct {
	// LeaseID is the unique lease identifier assigned at claim time.
	LeaseID string `json:"lease_id"`

	// JobID is the canonical job identifier this lease is for.
	JobID string `json:"job_id"`

	// WorkerID identifies the worker that holds this lease.
	WorkerID string `json:"worker_id"`

	// Attempt is the job attempt counter at the time the lease was
	// claimed. Must match the job's current attempt for the commit
	// to succeed.
	Attempt int `json:"attempt"`

	// ExpiresAt is the UTC timestamp after which the lease is
	// considered expired.
	ExpiresAt time.Time `json:"expires_at"`
}

// Valid reports whether the lease has not yet expired.
func (l Lease) Valid() bool {
	return time.Now().UTC().Before(l.ExpiresAt)
}
