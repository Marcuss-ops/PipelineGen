package asset

import "testing"

// TestIndexState_ValidAcceptsCanonical10 locks in the canonical IndexState
// alphabet (Task 2, July 2026). The old 7-state contract (pre-Task 2)
// is expanded with EMBEDDING / EMBEDDED / EMBEDDING_FAILED /
// INDEXING_FAILED, plus NOT_INDEXABLE (FASE 3b). INDEX_PENDING and
// Legacy INDEX_PENDING and INDEX_FAILED have been removed by migration 189.
func TestIndexState_ValidAcceptsCanonical12(t *testing.T) {
	canonical := []IndexState{
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
	}
	for _, s := range canonical {
		t.Run(string(s), func(t *testing.T) {
			if !s.Valid() {
				t.Errorf("Valid(%q): want true for canonical state; got false", string(s))
			}
		})
	}
}

// TestIndexState_ValidRejectsLegacyLowercase pins the PR6 invariant
// that pre-PR6 lowercase values (the alphabet that previously lived
// in metadata_json.$.index_state) are explicitly NOT canonical. A
// worker reading such a row in migration 094's backfill window
// (post-deploy, pre-first-touch by a worker) will see Valid()=false
// and route the column read into the recovery path (reindex admin
// endpoint). This guards against a future accidental promotion of the
// legacy alphabet that would break the canonical 7-state contract.
func TestIndexState_ValidRejectsLegacyLowercase(t *testing.T) {
	legacy := []IndexState{
		IndexState("embedding"),
		IndexState("upserting"),
		IndexState("retrying"),
		IndexState("indexed"),
		IndexState("failed"),
		// And arbitrary lowercase strings that were never valid.
		"indexing_failed",
		"deleted",
		"",
	}
	for _, s := range legacy {
		t.Run("reject_"+string(s), func(t *testing.T) {
			if s == "" {
				// Empty string is technically not rejected by Valid
				// (rejects via explicit return false in the default
				// case) but has no semantic meaning. Pinning this so
				// a future refactor doesn't accidentally treat "" as
				// valid.
				if s.Valid() {
					t.Errorf("Valid(\"\"): want false (no semantic meaning); got true")
				}
				return
			}
			if s.Valid() {
				t.Errorf("Valid(%q): want false for legacy/non-canonical value; got true", string(s))
			}
		})
	}
}

// TestIndexState_IsTerminal pins the helper that IndexDeleteHandler
// and IndexingHandler use to short-circuit state-machine gates.
//
// Task 2 (July 2026): EMBEDDING_FAILED and INDEXING_FAILED are terminal.
// EMBEDDED is intentionally NOT terminal — it means "embeddings saved,
// ready for Qdrant upsert".
func TestIndexState_IsTerminal(t *testing.T) {
	terminal := []IndexState{
		StateNotIndexable, StateIndexed, StateDELETED,
		StateEmbeddingFailed, StateIndexingFailed,
	}
	for _, s := range terminal {
		t.Run(string(s)+"_is_terminal", func(t *testing.T) {
			if !s.IsTerminal() {
				t.Errorf("IsTerminal(%q): want true; got false", string(s))
			}
		})
	}

	nonTerminal := []IndexState{
		StateDiscovered, StateEmbedding, StateEmbedded, StateIndexing,
		StateIndexDeletePending,
	}
	for _, s := range nonTerminal {
		t.Run(string(s)+"_not_terminal", func(t *testing.T) {
			if s.IsTerminal() {
				t.Errorf("IsTerminal(%q): want false; got true", string(s))
			}
		})
	}
}

// TestIndexState_IsFailedTerminal pins that EMBEDDING_FAILED,
// INDEXING_FAILED, and the legacy INDEX_FAILED all report as failed
// terminals. INDEXED is a successful terminal; DELETED is an
// intentional terminal (tombstone).
func TestIndexState_IsFailedTerminal(t *testing.T) {
	failed := []IndexState{StateEmbeddingFailed, StateIndexingFailed}
	for _, s := range failed {
		t.Run(string(s)+"_is_failed", func(t *testing.T) {
			if !s.IsFailedTerminal() {
				t.Errorf("IsFailedTerminal(%q): want true; got false", string(s))
			}
		})
	}

	notFailed := []IndexState{
		StateNotIndexable, StateIndexed, StateDELETED,
		StateDiscovered, StateEmbedding, StateEmbedded, StateIndexing,
		StateIndexDeletePending,
	}
	for _, s := range notFailed {
		t.Run(string(s)+"_not_failed", func(t *testing.T) {
			if s.IsFailedTerminal() {
				t.Errorf("IsFailedTerminal(%q): want false; got true", string(s))
			}
		})
	}
}

// TestIndexState_IsDeletedCanonical pins the helper IndexDeleteHandler
// uses to recognise tombstone-shaped states. DELETE_PENDING covers
// the in-flight window between the SetIndexState('DELETE_PENDING')
// flip and the SetIndexState('DELETED') flip; DELETED is the post-
// commit tombstone. Both should report true so a future
// idempotency_state check (not yet wired in PR6 but planned for
// QDRANT-005 followup) treats both as "deletion already engaged".
func TestIndexState_IsDeletedCanonical(t *testing.T) {
	deleted := []IndexState{StateIndexDeletePending, StateDELETED}
	for _, s := range deleted {
		t.Run(string(s)+"_is_deleted", func(t *testing.T) {
			if !s.IsDeletedCanonical() {
				t.Errorf("IsDeletedCanonical(%q): want true; got false", string(s))
			}
		})
	}

	notDeleted := []IndexState{
		StateDiscovered, StateEmbedding, StateEmbedded, StateIndexing,
		StateIndexed,
		StateNotIndexable, StateEmbeddingFailed, StateIndexingFailed,
	}
	for _, s := range notDeleted {
		t.Run(string(s)+"_not_deleted", func(t *testing.T) {
			if s.IsDeletedCanonical() {
				t.Errorf("IsDeletedCanonical(%q): want false; got true", string(s))
			}
		})
	}
}

// TestIndexState_StringLiteralValues is a regression test against an
// accidental capitalisation drift. Migration 094's backfill and the
// outbox event-key derivation both pin the literal string values —
// changing "DISCOVERED" to "Discovered" would silently break
// backfill CASE WHEN branches.
func TestIndexState_StringLiteralValues(t *testing.T) {
	cases := []struct {
		state IndexState
		want  string
	}{
		{StateDiscovered, "DISCOVERED"},
		{StateEmbedding, "EMBEDDING"},
		{StateEmbedded, "EMBEDDED"},
		{StateIndexing, "INDEXING"},
		{StateIndexed, "INDEXED"},
		{StateEmbeddingFailed, "EMBEDDING_FAILED"},
		{StateIndexingFailed, "INDEXING_FAILED"},
		{StateIndexDeletePending, "DELETE_PENDING"},
		{StateDELETED, "DELETED"},
		{StateNotIndexable, "NOT_INDEXABLE"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if string(c.state) != c.want {
				t.Errorf("state literal: want %q got %q", c.want, string(c.state))
			}
		})
	}
}
