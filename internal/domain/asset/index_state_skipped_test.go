// Package asset — index_state_skipped_test.go (PR-QDRANT-INDEXCLIP-GUARD,
// July 2026)
//
// Test 5 (state-machine-extension) for the new INDEXING_SKIPPED_NO_INDEXER
// value + IsRetryPending() predicate added on top of the
// canonical 13-state alphabet (Task 2, July 2026).
//
// Lives in the asset package per godlike/06 SSOT (one canonical
// owner per fact — the IndexState enum + Valid/IsTerminal/IsRetryPending
// predicates are owned by the asset package, not by individual
// consumers like the outbox handler). The companion handler-level
// tests live in internal/application/jobs/outbox/indexing_disabled_test.go.
//
// Separate file from the canonical index_state_test.go so a future
// PR that rotates this state out (e.g. into a "previous_state"
// audit column after operator-driven re-indexing tooling lands)
// can git-rm THIS file without touching the alphabet-pinning tests.
package asset

import "testing"

// TestIndexState_StateMachineExt_IndexingSkippedNoIndexer pins the
// new 13th state alongside the existing 12 (Task 2 alphabet +
// the PR-QDRANT-INDEXCLIP-GUARD extension). Pins:
//
//   - Valid()          → true (canonical)
//   - IsRetryPending() → true (transient retry-pending marker)
//   - IsTerminal()     → false (NOT a terminal — event stays in
//     outbox pool's pending bucket)
//   - IsFailedTerminal → false (NOT a failure terminal — operator
//     is NOT required to intervene; the
//     retry path advances automatically
//     when the indexer is re-enabled)
//   - IsDeletedCanonical → false (NOT a tombstone)
//
// The new state lives in the 13-state canonical alphabet; the
// existing TestIndexState_ValidAcceptsCanonical12 lists 12 (test
// was written pre-extension). This file's purpose is to lock the
// NEW state's semantics IN ISOLATION — a future refactor that
// renames the type literal must update BOTH this test and
// port-aware callers (outbox/indexing_disabled_test.go + the
// canonical 12 if you choose to bump its count).
func TestIndexState_StateMachineExt_IndexingSkippedNoIndexer(t *testing.T) {
	s := StateIndexingSkippedNoIndexer

	t.Run("literal_value", func(t *testing.T) {
		if string(s) != "INDEXING_SKIPPED_NO_INDEXER" {
			t.Errorf("literal: want %q, got %q", "INDEXING_SKIPPED_NO_INDEXER", string(s))
		}
	})

	t.Run("valid_canonical", func(t *testing.T) {
		if !s.Valid() {
			t.Errorf("Valid(%q): want true for canonical state; got false", string(s))
		}
	})

	t.Run("is_retry_pending", func(t *testing.T) {
		if !s.IsRetryPending() {
			t.Errorf("IsRetryPending(%q): want true (transient retry-pending marker); got false", string(s))
		}
	})

	t.Run("is_NOT_terminal", func(t *testing.T) {
		// godlike/07 fail-closed: a row in INDEXING_SKIPPED_NO_INDEXER
		// is mid-loop, NOT done. Pool.processEvent's IsTerminal
		// classifier MUST NOT classify this state as terminal —
		// otherwise MarkCompleted would silently absorb the retry
		// signal into "success".
		if s.IsTerminal() {
			t.Errorf("IsTerminal(%q): want false (retry-pending ≠ terminal); got true", string(s))
		}
	})

	t.Run("is_NOT_failed_terminal", func(t *testing.T) {
		// godlike/06: this state is a transient marker, NOT an
		// operator-must-intervene failure. IsFailedTerminal is the
		// predicate for the latter — must report false here.
		if s.IsFailedTerminal() {
			t.Errorf("IsFailedTerminal(%q): want false (transient marker ≠ failure terminal); got true", string(s))
		}
	})

	t.Run("is_NOT_deleted_canonical", func(t *testing.T) {
		// Sanity: a non-deleted state must report false on the
		// deletion helper (defence against future alphabet
		// collisions that would silently fold skip states into
		// tombstone shapes).
		if s.IsDeletedCanonical() {
			t.Errorf("IsDeletedCanonical(%q): want false; got true", string(s))
		}
	})
}

// TestIndexState_IsRetryPending_RejectsAllOtherStates pins that the
// IsRetryPending() predicate is exclusive — only
// StateIndexingSkippedNoIndexer returns true. Pre-existing alphabet
// states (including the deprecated INDEX_PENDING) report false so a
// future "auto-promote INDEX_PENDING to retry-pending on retry" PR
// cannot accidentally collide with the new state semantics.
func TestIndexState_IsRetryPending_RejectsAllOtherStates(t *testing.T) {
	nonRetryPending := []IndexState{
		StateNotIndexable,
		StateDiscovered,
		StateEmbedding,
		StateEmbedded,
		StateIndexing,
		StateIndexed,
		StateEmbeddingFailed,
		StateIndexingFailed,
		StateIndexDeletePending,
		StateDELETED,
		StateIndexPending, // deprecated — must NOT collide with new state
		StateIndexFailed,  // deprecated
	}
	for _, s := range nonRetryPending {
		t.Run(string(s)+"_not_retry_pending", func(t *testing.T) {
			if s.IsRetryPending() {
				t.Errorf("IsRetryPending(%q): want false (only INDEXING_SKIPPED_NO_INDEXER is retry-pending); got true", string(s))
			}
		})
	}
}
