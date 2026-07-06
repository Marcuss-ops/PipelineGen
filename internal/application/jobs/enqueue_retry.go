// Package jobs — enqueue_retry.go: strict typed MaxRetries resolution.
//
// PR-jobs-retry-contract (July 2026): the legacy hard-coded 3-retry
// fallback for unregistered job types is REMOVED from this codebase.
// The typed lookup via Registry.GetMaxRetries is the canonical SSOT;
// unknown jobTypes return ErrMaxRetriesUnknown — the caller (Enqueue)
// propagates the error so a missing registration is loud, NOT silenced
// by a legacy fallback (godlike/07 NO-FAKE-AVAILABILITY).
//
// 2026-07-06 (Phase 1 decomposition): split from enqueue_service.go per
// the god-object decomposition plan. Zero behavior changes. Same-package
// visibility preserves all caller paths; Enqueue calls s.resolveMaxRetries
// with no import changes.
package jobs

// resolveMaxRetries encodes the strict typed MaxRetries fallback
// semantic in a single testable helper. Enqueue() delegates to this
// helper so the logic is decoupled from repo/dispatcher concerns
// (test fixtures only need typed Service+Registry wiring).
//
// Three-way semantics, in priority order:
//
//  1. currentMR < 0  → 0      (explicit "no retries" sentinel —
//     pre-Issue-4 behavior preserved verbatim).
//
//  2. currentMR > 0  → currentMR  (caller pre-set value preserved
//     verbatim; registry is the fallback, not an override).
//
//  3. currentMR == 0 → registry.GetMaxRetries(jobType) (strict
//     typed lookup; the registry MUST already be attached at
//     construction time per the 4-arg NewService fail-closed
//     constructor). Unknown jobTypes return ErrMaxRetriesUnknown —
//     the caller (Enqueue) propagates the error so a missing
//     registration is loud, NOT silenced by a legacy 3-retry
//     fallback (PR-jobs-retry-contract removes the legacy
//     `return 3` line per godlike/07 NO-FAKE-AVAILABILITY).
//
// godlike/06 SSOT: this strict lookup supersedes the pre-PR
// hasRegistry() guard + the legacy `return 3` line. Removing those
// shapes eliminates two silent-success surfaces in one sweep.
func (s *Service) resolveMaxRetries(jobType string, currentMR int) (int, error) {
	if currentMR < 0 {
		return 0, nil
	}
	if currentMR > 0 {
		return currentMR, nil
	}
	// currentMR == 0 — single typed lookup.
	if s.registry == nil {
		// Defense-in-depth — should be unreachable given 4-arg NewService.
		return 0, ErrRegistryRequired
	}
	return s.registry.GetMaxRetries(jobType)
}
