package asset

import "testing"

// TestIndexState_ValidAcceptsCanonicalSeven locks in the exact 7-state
// alphabet that migration 094's ALTER TABLE ... DEFAULT 'DISCOVERED'
// + the backfill CASE expression pins. Any drift here is a contract
// break — the backfill would write a wrong value and the column
// would accept a non-canonical enum.
func TestIndexState_ValidAcceptsCanonicalSeven(t *testing.T) {
	canonical := []IndexState{
		StateDiscovered,
		StateIndexPending,
		StateIndexing,
		StateIndexed,
		StateIndexFailed,
		StateDeletePending,
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
		legacyStateEmbedding,
		legacyStateUpserting,
		legacyStateRetrying,
		legacyStateIndexed,
		legacyStateFailed,
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
// and IndexingHandler use to short-circuit state-machine gates
// ("is this row already at a terminal state? skip the transition").
// A failed terminal (INDEX_FAILED) is intentionally NOT terminal in
// the IsTerminal() sense — it represents an operator-must-intervene
// state, distinguished by IsFailedTerminal() below.
func TestIndexState_IsTerminal(t *testing.T) {
	terminal := []IndexState{
		StateIndexed, StateIndexFailed, StateDELETED,
	}
	for _, s := range terminal {
		t.Run(string(s)+"_is_terminal", func(t *testing.T) {
			if !s.IsTerminal() {
				t.Errorf("IsTerminal(%q): want true; got false", string(s))
			}
		})
	}

	nonTerminal := []IndexState{
		StateDiscovered, StateIndexPending, StateIndexing, StateDeletePending,
	}
	for _, s := range nonTerminal {
		t.Run(string(s)+"_not_terminal", func(t *testing.T) {
			if s.IsTerminal() {
				t.Errorf("IsTerminal(%q): want false; got true", string(s))
			}
		})
	}
}

// TestIndexState_IsFailedTerminal pins that only INDEX_FAILED reports
// as a failed-terminal. INDEXED is a successful terminal; DELETED is
// an intentional terminal (tombstone). StateIndexFailed is the only
// "must manually reindex" signal.
func TestIndexState_IsFailedTerminal(t *testing.T) {
	if !StateIndexFailed.IsFailedTerminal() {
		t.Error("StateIndexFailed must report IsFailedTerminal=true")
	}
	for _, s := range []IndexState{
		StateIndexed, StateDELETED, // successful terminals
		StateDiscovered, StateIndexPending, StateIndexing, StateDeletePending, // non-terminal
	} {
		t.Run(string(s)+"_not_failed_terminal", func(t *testing.T) {
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
	deleted := []IndexState{StateDeletePending, StateDELETED}
	for _, s := range deleted {
		t.Run(string(s)+"_is_deleted", func(t *testing.T) {
			if !s.IsDeletedCanonical() {
				t.Errorf("IsDeletedCanonical(%q): want true; got false", string(s))
			}
		})
	}

	notDeleted := []IndexState{
		StateDiscovered, StateIndexPending, StateIndexing,
		StateIndexed, StateIndexFailed,
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
		{StateIndexPending, "INDEX_PENDING"},
		{StateIndexing, "INDEXING"},
		{StateIndexed, "INDEXED"},
		{StateIndexFailed, "INDEX_FAILED"},
		{StateDeletePending, "DELETE_PENDING"},
		{StateDELETED, "DELETED"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if string(c.state) != c.want {
				t.Errorf("state literal: want %q got %q", c.want, string(c.state))
			}
		})
	}
}
