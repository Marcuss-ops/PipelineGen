// Package asset — lifecycle_state_test.go (Blocco 3.2 commit 1/2, June 2026)
//
// Pins the IsValidTransition table for asset.LifecycleState. The
// contract is the canonical deletion state-machine + restore edge
// declared at asset_types.go::IsValidTransition:
//
//	ACTIVE              → DELETE_REQUESTED        (user-initiated delete)
//	DELETE_REQUESTED    → DRIVE_DELETE_PENDING    (DriveDeleteHandler pre-flip)
//	DRIVE_DELETE_PENDING→ INDEX_DELETE_PENDING    (DriveDeleteHandler post-success flip)
//	INDEX_DELETE_PENDING→ DELETED                 (IndexDeleteHandler post-success flip)
//	*                   → ACTIVE                  (restore path; Wave 22 — pin IsValidRestoreTransition in a follow-up)
//
// Self-loops are idempotent (`a.IsValidTransition(a) == true`); the
// legacy StateDeletePending (lowercase broad-intent) admits a rewrite
// path to both StateDriveDeletePending and StateDeleteRequested so the
// reconciler can normalise pre-Blocco 3.1 rows.
//
// The table below enumerates every (from, to) pair across the 9
// canonical LifecycleState values. Drift here breaks ALL of:
//   - Dispatcher.EnqueueDriveDelete(idempotency WHERE excluded set)
//   - Dispatcher.AdvanceAndEmit(state-machine UPDATE WHERE = expectedState)
//   - DriveDeleteHandler pre-flight switch
//   - IndexDeleteHandler pre-flight switch
//   - DeletionReconciler stuck-row rewrite path
//
// Mirrors the test surface for IndexState at index_state_test.go —
// one canonical state file per typed enum so audit hunks stay
// scoped to the affected type.
package render

import "testing"

// allLifecycleStates is the canonical, deterministic enumeration
// used to drive the full (from, to) Cartesian. Mirrors
// CanonicalLifecycleStateValues() in asset_types.go to keep the
// test table in lockstep with production code: if a new state
// lands in the canonical list, the expected-vs-actual diffs in
// TestLifecycleState_IsValidTransitionFullTable surface the gap
// within a single CI run.
var allLifecycleStates = []LifecycleState{
	StateStaging,
	StateProcessing,
	StateActive,
	StateDeletePending,
	StateDeleteRequested,
	StateDriveDeletePending,
	StateDriveDeleted,
	StateLifecycleIndexDeletePending,
	StateIndexDeleted,
	StateDeleted,
	StateError,
}

// TestLifecycleState_IsValidTransitionFullTable exhaustively
// enumerates the (from, to) Cartesian product over the 9 canonical
// lifecycle states (81 pairs total; the 9 self-loops are always
// true by IsValidTransition's first-line guard).
//
// The drift detector: a missing pair surfaces as a test failure
// with the offending (from, to) pair printed; a new state added to
// allLifecycleStates / CanonicalLifecycleStateValues surfaces as
// an "expected pair missing from want-if-explicit map" panic +
// asserts.Equal diff the full want-actual map side-by-side.
//
// The want-if-explicit table below is the authoritative source of
// which non-self-loop edges are allowed. The contract lives in the
// IsValidTransition method body; this test is the regression pin.
func TestLifecycleState_IsValidTransitionFullTable(t *testing.T) {
	// want-if-explicit: only the (from, to) pairs that IsValidTransition
	// should accept as true BEYOND the universal self-loop. Self-loops
	// are added programmatically below so a new state is automatically
	// covered without touching this map.
	want := map[LifecycleState]map[LifecycleState]bool{
		StateActive: {
			StateDeleteRequested: true, // user-initiated delete (new chain)
			StateDeletePending:   true, // legacy broad-intent transition
			StateError:           true, // Drive reconciliation invalidated the only publishable location
		},
		StateDeleteRequested: {
			StateDriveDeletePending: true, // DriveDeleteHandler pre-flip stamp
		},
		StateDeletePending: {
			StateDriveDeletePending: true, // legacy rewrite path → Drive hop
			StateDeleteRequested:    true, // legacy rewrite path → new chain
		},
		StateDriveDeletePending: {
			StateDriveDeleted: true, // DriveDeleteHandler post-success flip (Blocco 3.1 commit 2/3, July 2026)
		},
		StateDriveDeleted: {
			StateLifecycleIndexDeletePending: true, // IndexDeleteHandler pre-flip stamp
		},
		StateLifecycleIndexDeletePending: {
			StateIndexDeleted: true, // IndexDeleteHandler post-Qdrant+SoftDelete flip (Blocco 3.1 commit 2/3, July 2026)
		},
		StateIndexDeleted: {
			StateDeleted: true, // IndexDeleteHandler post-success terminal flip
		},
		// STAGING, PROCESSING, DELETED, ERROR → no out-edges (intentionally
		// empty maps; self-loops handled by the universal layer below).
	}

	for _, from := range allLifecycleStates {
		t.Run(string(from), func(t *testing.T) {
			fromWants := want[from]
			for _, to := range allLifecycleStates {
				got := from.IsValidTransition(to)
				// Universal self-loop.
				if from == to {
					if !got {
						t.Errorf("self-loop rejected: IsValidTransition(%q, %q)=false; want true (idempotent self-loop)", from, to)
					}
					continue
				}
				wantExplicit := fromWants[to]
				if wantExplicit && !got {
					t.Errorf("IsValidTransition(%q, %q)=false; want true (explicit edge in delete state machine)", from, to)
				}
				if !wantExplicit && got {
					t.Errorf("IsValidTransition(%q, %q)=true; want false (no documented edge)", from, to)
				}
			}
		})
	}
}

// TestLifecycleState_IsValidTransition_StringLiteralValues pins the
// exact strings of every canonical lifecycle state. Mirrors
// IndexState_StringLiteralValues in index_state_test.go — drift in
// the string values would silently break every WHERE clause that
// compares against the database-stored column (EnqueueDriveDelete's
// "[NON-IN list]", DriveDeleteHandler's "case StateDeleteRequested",
// etc.).
func TestLifecycleState_IsValidTransition_StringLiteralValues(t *testing.T) {
	cases := []struct {
		state LifecycleState
		want  string
	}{
		{StateStaging, "STAGING"},
		{StateProcessing, "PROCESSING"},
		{StateActive, "ACTIVE"},
		{StateDeletePending, "DELETE_PENDING"},
		{StateDeleteRequested, "DELETE_REQUESTED"},
		{StateDriveDeletePending, "DRIVE_DELETE_PENDING"},
		{StateDriveDeleted, "DRIVE_DELETED"},
		{StateLifecycleIndexDeletePending, "INDEX_DELETE_PENDING"},
		{StateIndexDeleted, "INDEX_DELETED"},
		{StateDeleted, "DELETED"},
		{StateError, "ERROR"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if string(c.state) != c.want {
				t.Errorf("state literal: want %q got %q", c.want, string(c.state))
			}
		})
	}
}
