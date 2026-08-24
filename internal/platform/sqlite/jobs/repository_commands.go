package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Errors ──────────────────────────────────────────────────────────────
//
// godlike/06 SSOT (one canonical owner per fact) — Fase 5(b)
// cutover (P0.F regression surface, July 2026):
//
//   - The CANONICAL declarations of ErrLeaseLost, ErrTransitionConflict,
//     and ErrJobNotFound live in internal/domain/job/errors.go (Fase 5(a)
//     homing; godlike/06 SSOT). The 3 re-export aliases below share the
//     same `error` value with the canonical decls (`var X = job.X` is a
//     Go var-alias, not a wrapper) so `errors.Is(err, jobs.ErrLeaseLost)`
//     and `errors.Is(err, job.ErrLeaseLost)` probe the SAME sentinel.
//     Identity preservation is load-bearing for tests/callers that
//     errors.Is any of the 3 (no call-site migration required).
//
//   - ErrAlreadyClaimed, ErrInvalidState, ErrInvalidResultJSON stay as
//     package-LOCAL sentinels: their failure shapes are
//     infra-layer-specific (encode/decode of `job.Status` enum or
//     SQLite-typed-column dual-write semantics) and have no canonical
//     cross-package contract — promoting them to domjob would expose
//     infra-layer detail as a domain-level public surface (noise that
//     the Fase 5(a) cutover was designed to reduce).
//
// Pre-Fase-5 history (forward-prevention, NOT a code path): the
// `ErrLeaseLost` + `ErrTransitionConflict` declarations used to live
// in `store.go` (Wave 17.1.2 canonical-home target). Fase 5(a) moved
// the canonical decls to domjob and Fase 5(b) was supposed to leave
// transparent re-export aliases in the sqlite package — but the alias
// re-export step was never completed. The half-applied cutover left
// `lifecycle_*.go` / `finalize_attempt.go` referencing the names
// unprefixed and the entire `internal/platform/sqlite/jobs/`
// package failed to build. The aliases below complete the cutover
// correctly: re-export (not re-declare) so identity is preserved.
var (
	// Package-LOCAL sentinels (no canonical decl in domjob).
	ErrAlreadyClaimed    = errors.New("job already claimed by another worker")
	ErrInvalidState      = errors.New("invalid state transition")
	ErrInvalidResultJSON = errors.New("finalize aggregate parent: result JSON malformed (cannot extract parent_state for typed-column dual-write)")

	// Re-export aliases of internal/domain/job/errors.go (Fase 5(b) cutover
	// completion). godlike/06 SSOT: same `error` value as canonical decl;
	// errors.Is probes are symmetric across import paths.
	ErrLeaseLost          = job.ErrLeaseLost
	ErrTransitionConflict = job.ErrTransitionConflict
	ErrJobNotFound        = job.ErrJobNotFound
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
