// Package operations — generation_submission_observability.go owns the
// post-remediation lock-contention observability surface for Submit.
//
// SUBMIT-LOCK-INSTRUMENTATION (September 2026): the P1 submission-mutex
// remediation narrowed the submitMu critical section to
// lookup + decision + write (the advisory JobGetter read moved outside).
// This file exposes the residual contention numbers the concurrency
// analysis asked to measure BEFORE any further refactor:
//
//   - submit_lock_wait_ms — cumulative + per-call time goroutines spent
//     acquiring submitMu (the production signal for "does a slow
//     submission still serialise unrelated ones?").
//   - submit_hold_count   — number of Submit calls that entered the
//     mutex section (denominator for the wait average).
package operations

import "time"

// SubmitLockStats is a point-in-time snapshot of the submission mutex
// contention counters. Pure read: safe to call from any goroutine,
// including admin/diagnostic handlers.
type SubmitLockStats struct {
	// LockWaitTotal is the cumulative time all Submit calls spent
	// acquiring submitMu (nanoseconds; the caller renders the ms unit).
	LockWaitTotal time.Duration
	// HoldCount is the number of Submit calls that entered the mutex
	// section since process start. Zero when no Submit ran yet.
	HoldCount int64
}

// AverageLockWait returns the mean mutex wait per mutex-entering Submit
// call. Zero when no Submit call has entered the section yet.
func (s SubmitLockStats) AverageLockWait() time.Duration {
	if s.HoldCount <= 0 {
		return 0
	}
	return s.LockWaitTotal / time.Duration(s.HoldCount)
}

// SubmitLockStats snapshots the current contention counters. Wiring note:
// admin/diagnostics surfaces should call this (not read the fields
// directly) so the atomic sampling stays in one place.
func (s *Service) SubmitLockStats() SubmitLockStats {
	return SubmitLockStats{
		LockWaitTotal: time.Duration(s.submitLockWaitNanos.Load()),
		HoldCount:     s.submitHoldCount.Load(),
	}
}
