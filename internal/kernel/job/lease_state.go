// Package job — lease_state.go: canonical LeaseState enum +
// RenewLeaseResult struct (Fase 4(b), July 2026).
//
// Per godlike/06 SSOT (one canonical observer per fact), the
// canonical lease-renewal primitive (kernel.Store.RenewLease) 
// returns LeaseState so the caller can compose the lease-renewal 
// path with concurrent cancellation in a SINGLE SQL UPDATE — 
// eliminating the per-job ticker goroutine AND the per-job 
// IsCancelled poll that pre-Fase-4 required.
//
// This file declares ONLY the typed surface (LeaseState enum,
// RenewLeaseResult struct, RenewLeaseResult.NewExpiry state-
// conditional accessor). The store-interface signature changes 
// land in Push 4.3; today the canonical kernel.Store.RenewLease
// signature is unchanged and the post-Fase-4 workers continue to
// log-and-continue based on error return value.
package job

import (
	"time"
)

// LeaseState is the canonical typed result of a RenewLease attempt.
//
// godlike/07 fail-closed: the SQL-layer harness guarantees that
// every RenewLease call lands in EXACTLY ONE of the three enum 
// values. The LeaseState is NOT derived from a separate SELECT 
// after-the-UPDATE (that approach would reintroduce the lost-update
// race between the renewal transaction and the operator-set
// cancelled_at column); instead the LeaseState is the typed 
// canonical encoding of the SQL UPDATE's three-way filter result:
//
//	UPDATE jobs SET lease_expiry = ?
//	WHERE id = ? AND worker_id = ?
//	RETURNING
//	  CASE
//	    WHEN lease_expiry > now AND cancelled_at IS NULL THEN 'continue'
//	    WHEN cancelled_at IS NOT NULL THEN 'cancel_requested'
//	    ELSE 'lease_lost'  -- no rows updated, job stolen/expired
//	  END
//
// godlike/06 SSOT: lease-stolen is distinguished from worker-
// mismatch (pre-Fase-4 typed as ErrLeaseLost) — they land on the
// same sentinel to keep the pre-Fase-4 worker code compile-stable,
// but the LeaseState value provides the worker with a typed hint
// about WHY the renewal failed.
type LeaseState string

const (
	// LeaseStateContinue — lease successfully extended, NOT cancelled.
	// Caller MUST proceed with the in-flight job. The new expiry
	// is in RenewLeaseResult.NewLeaseExpiry (non-nil); the row's
	// revision is in RenewLeaseResult.JobRevision.
	LeaseStateContinue LeaseState = "continue"

	// LeaseStateCancelRequested — jobs.cancelled_at is NOT NULL.
	// Caller MUST abort the in-flight job (clean shutdown, return
	// partial state, do NOT call FinalizeAttempt on this job —
	// terminal transitions on a cancelled job surface as 
	// ErrAlreadyTerminal at the SQL-layer fence, godlike/07 
	// fail-closed).
	//
	// Source of cancellation (operator action vs sibling pipeline
	// step cancellation): eyes-out of scope for this file. The
	// canonical Cancel primitive lives at store.go::Cancel; it
	// is the sole writer of jobs.cancelled_at outside of pre-Fase-4
	// aggregation paths.
	LeaseStateCancelRequested LeaseState = "cancel_requested"

	// LeaseStateLeaseLost — lease stolen by another worker, or
	// lease expired and requeued by the reaper (row no longer 
	// matches worker_id). Caller MUST treat the in-flight work
	// as orphaned and abort. The SQL-layer harness surfaces
	// ErrLeaseLost alongside the typed LeaseState value so 
	// errors.Is(err, ErrLeaseLost) probes work symmetrically 
	// with the pre-Fase-4 RenewLease failure semantics.
	//
	// The ErrLeaseLost companion error is the canonical sentinel
	// defined in the SQL implementation layer (kernel-side
	// abstract error is forward-pointer; pre-Fase-4 callers use
	// sqljobs.ErrLeaseLost which is the canonical concrete one).
	LeaseStateLeaseLost LeaseState = "lease_lost"
)

// RenewLeaseResult is the typed-narrow return from RenewLease.
//
// godlike/07 minimum-blast-radius: every field is conditional on
// State == LeaseStateContinue. For non-Continue states the field
// values are zero-values; the NewExpiry() state-conditional accessor
// shields callers from typo-driven nil-deref.
//
// godlike/06 SSOT (one canonical surface per fact): the 
// NewLeaseExpiry + JobRevision fields are populated ONLY by the
// SQL layer's RETURNING clause in post-Push-4.3 implementations.
// Pre-Fase-4 callers (today) ignore the typed envelope and observe
// the boolean error path; the post-Push-4.3 worker transition is
// incremental.
type RenewLeaseResult struct {
	// State is the canonical lease state. Required; non-empty.
	// Must be one of the LeaseState* enum values; unknown values
	// (e.g. from a stale SQL adapter) surface as godlike/07
	// fail-closed panic at the SQL-layer decode (forward-pointer
	// to internal/sqljobs/lease_decoder.go).
	State LeaseState

	// NewLeaseExpiry is the post-renewal lease_expiry timestamp.
	// Non-nil iff State == LeaseStateContinue. The state-
	// conditional accessor NewExpiry() (below) shields callers
	// from consulting this field on non-Continue states.
	//
	// Semantic mirror of pre-Fase-4 lease_expiry: the field is
	// the SQL row's jobs.lease_expiry column after the SQL 
	// UPDATE commits. Workers can use it to compute the next
	// "must renew by" timestamp without a second roundtrip.
	NewLeaseExpiry *time.Time

	// JobRevision is the row's post-renewal revision. The caller
	// MUST NOT update its expectedRevision snapshot based on this
	// value: pre-Fase-4, RenewLease did NOT bump revision (per
	// the JOBS-T01-SQLITE-REPO canonical invariant, June 2026).
	// Fase 4 preserves the invariant — JobRevision equals the
	// pre-call revision. The field is exposed for future Fase-5
	// expansion (a future commit may add revision-bumping for
	// renewals without breaking this contract — callers MUST
	// already be using the CAS-update pattern on lease-rev when
	// a forward-pointer PR lands).
	//
	// Until then, callers can consult this field for diagnostic
	// sanity ("did the renewal preserve my revision?") but MUST
	// NOT use it to update the lease-fence snapshot.
	JobRevision int
}

// NewExpiry returns the new lease expiry iff State == LeaseStateContinue.
// State-conditional accessor that shields callers from typing a 
// direct nil-deref when the renewal fell into LeaseStateLeaseLost 
// or LeaseStateCancelRequested. Returns (time.Time{}, false) for 
// non-Continue states; the bool is the canonical "valid" flag.
//
// godlike/07 fail-closed: a worker process that reads the timestamp
// for LeaseStateLeaseLost would proceed with jobs owned by another
// worker — a SEV-1 blast-radius event. The accessor makes that 
// pattern impossible to write by accident; pre-Fase-4 the 
// equivalent pattern was `re.NewLeaseExpiry.IsZero()` which is
// structurally identical but allowed silent-default false readings.
func (r RenewLeaseResult) NewExpiry() (time.Time, bool) {
	if r.State != LeaseStateContinue || r.NewLeaseExpiry == nil {
		return time.Time{}, false
	}
	return *r.NewLeaseExpiry, true
}

// IsValid reports whether s is one of the canonical LeaseState values.
// godlike/07 fail-closed: unknown wire values are NOT silently collapsed
// — the SQL-layer fence rejects them with ErrUnknownLeaseState
// (forward-pointer, declared in Push 4.3). This Go-side helper is the
// compile-time + runtime first line of defence; callers / tests can
// call IsValid() without round-tripping through the SQL adapter.
func (s LeaseState) IsValid() bool {
	switch s {
	case LeaseStateContinue, LeaseStateCancelRequested, LeaseStateLeaseLost:
		return true
	}
	return false
}
