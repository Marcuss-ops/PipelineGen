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
