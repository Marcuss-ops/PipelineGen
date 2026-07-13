// Package asset — asset_state_test.go (PR-CATALOG-MULTILINGUA
// step 7, July 2026)
//
// Pins the IsValidTransition table for asset.AssetState plus
// the helper predicates. The contract is the canonical 14-
// state machine declared at asset_state.go::IsValidTransition:
//
//	Happy path (11 forward edges):
//	    DISCOVERED    → DOWNLOADED
//	    DOWNLOADED    → NORMALIZED
//	    NORMALIZED    → HASHED
//	    HASHED        → UPLOADED
//	    UPLOADED      → TRANSCRIBED
//	    TRANSCRIBED   → ENRICHED
//	    ENRICHED      → TRANSLATED
//	    TRANSLATED    → INDEX_PENDING
//	    INDEX_PENDING → INDEXED
//	    INDEXED       → READY
//	    READY         → READY_MULTILINGUAL
//
//	Degradation: READY_MULTILINGUAL → READY
//
//	Failure exits: <any pre-terminal> → FAILED_RETRYABLE /
//	               FAILED_PERMANENT.
//
//	Retry re-entry: FAILED_RETRYABLE → <any pre-terminal>.
//
//	FAILED_PERMANENT: terminal, zero out-edges.
//
//	Self-loops idempotent across all 14 states.
//
// Mirrors the test discipline of pipeline_state_test.go (full
// 14×14 matrix + side-tests for each invariant). A future PR
// adding a 15th state surfaces as a test failure within a
// single CI run.
package asset

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// allAssetStates is the canonical, deterministic enumeration
// used to drive the full (from, to) Cartesian. Mirrors
// CanonicalAssetStateValues() in asset_state.go.
var allAssetStates = []AssetState{
	StateAssetDiscovered,
	StateAssetDownloaded,
	StateAssetNormalized,
	StateAssetHashed,
	StateAssetUploaded,
	StateAssetTranscribed,
	StateAssetEnriched,
	StateAssetTranslated,
	StateAssetIndexPending,
	StateAssetIndexed,
	StateAssetReady,
	StateAssetReadyMultilingual,
	StateAssetFailedRetryable,
	StateAssetFailedPermanent,
}

// happyPathAssetEdges is the explicit list of happy-path
// forward edges. Mirrors asset_state.go's IsValidTransition
// method body verbatim so a future refactor that drops an
// edge is caught by the matrix test below.
var happyPathAssetEdges = []struct{ from, to AssetState }{
	{StateAssetDiscovered, StateAssetDownloaded},
	{StateAssetDownloaded, StateAssetNormalized},
	{StateAssetNormalized, StateAssetHashed},
	{StateAssetHashed, StateAssetUploaded},
	{StateAssetUploaded, StateAssetTranscribed},
	{StateAssetTranscribed, StateAssetEnriched},
	{StateAssetEnriched, StateAssetTranslated},
	{StateAssetTranslated, StateAssetIndexPending},
	{StateAssetIndexPending, StateAssetIndexed},
	{StateAssetIndexed, StateAssetReady},
	{StateAssetReady, StateAssetReadyMultilingual},
}

// nonTerminalAssetStates is the 11-state set that admits a
// FAILED_* exit. The IsValidTransition source code mirrors
// this slice via the `canonicalPreTerminalStates` private
// package var (which the regression test
// TestAssetState_PreTerminalStatesLength pins as exactly 11
// in lockstep with this test-side mirror).
var nonTerminalAssetStates = []AssetState{
	StateAssetDiscovered,
	StateAssetDownloaded,
	StateAssetNormalized,
	StateAssetHashed,
	StateAssetUploaded,
	StateAssetTranscribed,
	StateAssetEnriched,
	StateAssetTranslated,
	StateAssetIndexPending,
	StateAssetIndexed,
	StateAssetReady,
}

// isExplicitlyAllowedAsset mirrors IsValidTransition's
// body. Pinning the truth table here means a future refactor
// that drops an edge OR adds an undocumented edge surfaces as
// a diff between the test's computed verdict and the
// production verdict.
func isExplicitlyAllowedAsset(from, to AssetState) bool {
	if from == to {
		return true // self-loop, only valid if both states are canonical.
	}
	if !from.Valid() || !to.Valid() {
		return false // zero/unknown rejection.
	}
	// Failure exits.
	if to == StateAssetFailedRetryable || to == StateAssetFailedPermanent {
		for _, nt := range nonTerminalAssetStates {
			if nt == from {
				return true
			}
		}
		return false
	}
	// Degradation: only READY_MULTILINGUAL → READY.
	if from == StateAssetReadyMultilingual {
		return to == StateAssetReady
	}
	// Retry re-entry from FAILED_RETRYABLE.
	if from == StateAssetFailedRetryable {
		for _, nt := range nonTerminalAssetStates {
			if nt == to {
				return true
			}
		}
		return false
	}
	// FAILED_PERMANENT is terminal.
	if from == StateAssetFailedPermanent {
		return false
	}
	// Happy-path forward edges.
	for _, e := range happyPathAssetEdges {
		if e.from == from && e.to == to {
			return true
		}
	}
	return false
}

// TestAssetState_IsValidTransitionFullTable exhaustively
// enumerates the (from, to) Cartesian over the 14 canonical
// states (196 pairs total; 14 self-loops always true).
// The contract lives in the IsValidTransition method body;
// this test is the regression pin.
func TestAssetState_IsValidTransitionFullTable(t *testing.T) {
	for _, from := range allAssetStates {
		t.Run(string(from), func(t *testing.T) {
			for _, to := range allAssetStates {
				got := from.IsValidTransition(to)
				// Universal self-loop.
				if from == to {
					if !got {
						t.Errorf("self-loop rejected: IsValidTransition(%q, %q)=false; want true (idempotent self-loop)", from, to)
					}
					continue
				}
				want := isExplicitlyAllowedAsset(from, to)
				if want && !got {
					t.Errorf("IsValidTransition(%q, %q)=false; want true (explicit edge)", from, to)
				}
				if !want && got {
					t.Errorf("IsValidTransition(%q, %q)=true; want false (no documented edge)", from, to)
				}
			}
		})
	}
}

// TestAssetState_ReadyMultilingualEntryOnlyFromReady pins
// the invariant that no state skips past a successor —
// READY_MULTILINGUAL can ONLY be entered FORWARD from READY.
// Earlier states attempting to skip forward into
// READY_MULTILINGUAL are rejected (the readiness gate
// predicate is the only path that flips this state, and it
// does so only after the candidate is at READY).
//
// The test excludes TWO states from the loop body:
//
//   - StateAssetReady: the only state allowed to advance
//     forward into READY_MULTILINGUAL.
//   - StateAssetReadyMultilingual: the self-loop is
//     universally allowed (idempotent writes); it is NOT a
//     skip-forward entry, so the test's "entry from outside"
//     intent does not apply to it. The self-loop invariant
//     is covered verbatim by TestAssetState_IsValidTransition
//     FullTable which pins IsValidTransition(READY_MULTI
//     LINGUAL, READY_MULTILINGUAL) = true via the universal
//     self-loop guard.
func TestAssetState_ReadyMultilingualEntryOnlyFromReady(t *testing.T) {
	for _, from := range allAssetStates {
		if from == StateAssetReady {
			continue // READY → READY_MULTILINGUAL is the only valid FORWARD entry.
		}
		if from == StateAssetReadyMultilingual {
			continue // self-loop is universally allowed; not a "skip-forward entry".
		}
		if from.IsValidTransition(StateAssetReadyMultilingual) {
			t.Errorf("IsValidTransition(%q, READY_MULTILINGUAL)=true; want false (only READY may advance; self-loop is exempted via IsValidTransitionFullTable)", from)
		}
	}
	// Sanity: READY → READY_MULTILINGUAL is valid (the single forward entry).
	if !StateAssetReady.IsValidTransition(StateAssetReadyMultilingual) {
		t.Error("READY → READY_MULTILINGUAL must be valid")
	}
}

// TestAssetState_ReadyMultilingualDegradesToReady pins the
// degradation edge: READY_MULTILINGUAL → READY is the ONLY
// allowed out-edge from READY_MULTILINGUAL. FAILED_* edges
// are excluded because the success terminal is non-failure.
func TestAssetState_ReadyMultilingualDegradesToReady(t *testing.T) {
	if !StateAssetReadyMultilingual.IsValidTransition(StateAssetReady) {
		t.Error("READY_MULTILINGUAL → READY must be valid (degradation)")
	}
	for _, to := range allAssetStates {
		if to == StateAssetReadyMultilingual || to == StateAssetReady {
			continue
		}
		if StateAssetReadyMultilingual.IsValidTransition(to) {
			t.Errorf("READY_MULTILINGUAL → %q must NOT be valid (only READY is the degradation target)", to)
		}
	}
}

// TestAssetState_FailedPermanentIsTerminal pins that
// FAILED_PERMANENT has zero out-edges (except the self-loop).
func TestAssetState_FailedPermanentIsTerminal(t *testing.T) {
	for _, to := range allAssetStates {
		if to == StateAssetFailedPermanent {
			continue // self-loop handled separately.
		}
		if StateAssetFailedPermanent.IsValidTransition(to) {
			t.Errorf("FAILED_PERMANENT → %q must NOT be valid (FAILED_PERMANENT is terminal)", to)
		}
	}
}

// TestAssetState_FailedRetryableReEntry pins the matrix of
// re-entry edges from FAILED_RETRYABLE: into any of the 11
// pre-terminal states OR self-loop; nothing else.
func TestAssetState_FailedRetryableReEntry(t *testing.T) {
	for _, to := range allAssetStates {
		got := StateAssetFailedRetryable.IsValidTransition(to)
		if to == StateAssetFailedRetryable {
			if !got {
				t.Errorf("FAILED_RETRYABLE → FAILED_RETRYABLE (self-loop) must be valid")
			}
			continue
		}
		wantReEntry := false
		for _, nt := range nonTerminalAssetStates {
			if to == nt {
				wantReEntry = true
				break
			}
		}
		// The FAILED_* are not in the re-entry set —
		// re-entry is into happy-path states only.
		if to == StateAssetReadyMultilingual {
			wantReEntry = false
		}
		if wantReEntry && !got {
			t.Errorf("FAILED_RETRYABLE → %q=false; want true (retry re-entry)", to)
		}
		if !wantReEntry && got {
			t.Errorf("FAILED_RETRYABLE → %q=true; want false (outside retry re-entry set)", to)
		}
	}
}

// TestAssetState_StringLiteralValues pins the exact strings
// of every canonical AssetState. Drift in the string
// values would silently break every WHERE clause that
// compares against the media_assets.asset_state column.
func TestAssetState_StringLiteralValues(t *testing.T) {
	cases := []struct {
		state AssetState
		want  string
	}{
		{StateAssetDiscovered, "DISCOVERED"},
		{StateAssetDownloaded, "DOWNLOADED"},
		{StateAssetNormalized, "NORMALIZED"},
		{StateAssetHashed, "HASHED"},
		{StateAssetUploaded, "UPLOADED"},
		{StateAssetTranscribed, "TRANSCRIBED"},
		{StateAssetEnriched, "ENRICHED"},
		{StateAssetTranslated, "TRANSLATED"},
		{StateAssetIndexPending, "INDEX_PENDING"},
		{StateAssetIndexed, "INDEXED"},
		{StateAssetReady, "READY"},
		{StateAssetReadyMultilingual, "READY_MULTILINGUAL"},
		{StateAssetFailedRetryable, "FAILED_RETRYABLE"},
		{StateAssetFailedPermanent, "FAILED_PERMANENT"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			assert.Equal(t, c.want, string(c.state))
		})
	}
}

// TestAssetState_Valid pins the Valid() gate. Ad-hoc values
// must be rejected; canonical values must be accepted.
func TestAssetState_Valid(t *testing.T) {
	canonical := allAssetStates
	for _, s := range canonical {
		t.Run("accept_"+string(s), func(t *testing.T) {
			assert.True(t, s.Valid(), "Valid(%q): want true for canonical", string(s))
		})
	}
	invalid := []AssetState{
		AssetState(""),
		AssetState("discovered"),   // lowercase
		AssetState("DOWNLOAD"),     // truncated
		AssetState("DOWNLOADED_X"), // padded
		AssetState("FAILED "),      // trailing space
		AssetState(" INDEXED"),     // leading space
		AssetState("FOO_BAR"),      // bogus
		AssetState("READY_MULTI"),  // truncated MULTILINGUAL
	}
	for _, s := range invalid {
		t.Run("reject_"+string(s), func(t *testing.T) {
			assert.False(t, s.Valid(), "Valid(%q): want false for non-canonical", string(s))
		})
	}
}

// TestAssetState_IsTerminal pins the terminal-state gate.
// The 2 terminal states are READY_MULTILINGUAL +
// FAILED_PERMANENT. FAILED_RETRYABLE is NOT terminal
// (IsTerminal returns false; IsRetryable returns true).
func TestAssetState_IsTerminal(t *testing.T) {
	terminal := []AssetState{
		StateAssetReadyMultilingual,
		StateAssetFailedPermanent,
	}
	for _, s := range terminal {
		t.Run(string(s)+"_is_terminal", func(t *testing.T) {
			assert.True(t, s.IsTerminal(), "IsTerminal(%q): want true", string(s))
		})
	}
	nonTerminal := []AssetState{
		StateAssetDiscovered, StateAssetDownloaded, StateAssetNormalized,
		StateAssetHashed, StateAssetUploaded, StateAssetTranscribed,
		StateAssetEnriched, StateAssetTranslated, StateAssetIndexPending,
		StateAssetIndexed, StateAssetReady,
		StateAssetFailedRetryable,
	}
	for _, s := range nonTerminal {
		t.Run(string(s)+"_not_terminal", func(t *testing.T) {
			assert.False(t, s.IsTerminal(), "IsTerminal(%q): want false", string(s))
		})
	}
}

// TestAssetState_HelperMethods pins the 4 predicate methods
// (IsFailedTerminal, IsRetryable, IsSucceededTerminal,
// IsMultilingualGate). The matrix below exhaustively walks
// the 14 states × 4 methods table.
func TestAssetState_HelperMethods(t *testing.T) {
	cases := []struct {
		s                                   AssetState
		wantFailed, wantRetryable           bool
		wantSucceeded, wantMultilingualGate bool
	}{
		{StateAssetReadyMultilingual, false, false, true, true},
		{StateAssetFailedPermanent, true, false, false, false},
		{StateAssetFailedRetryable, false, true, false, false},
		{StateAssetDiscovered, false, false, false, false},
		{StateAssetDownloaded, false, false, false, false},
		{StateAssetNormalized, false, false, false, false},
		{StateAssetHashed, false, false, false, false},
		{StateAssetUploaded, false, false, false, false},
		{StateAssetTranscribed, false, false, false, false},
		{StateAssetEnriched, false, false, false, false},
		{StateAssetTranslated, false, false, false, false},
		{StateAssetIndexPending, false, false, false, false},
		{StateAssetIndexed, false, false, false, false},
		{StateAssetReady, false, false, false, false},
	}
	for _, c := range cases {
		t.Run(string(c.s), func(t *testing.T) {
			assert.Equal(t, c.wantFailed, c.s.IsFailedTerminal(), "IsFailedTerminal")
			assert.Equal(t, c.wantRetryable, c.s.IsRetryable(), "IsRetryable")
			assert.Equal(t, c.wantSucceeded, c.s.IsSucceededTerminal(), "IsSucceededTerminal")
			assert.Equal(t, c.wantMultilingualGate, c.s.IsMultilingualGate(), "IsMultilingualGate")
		})
	}
}

// TestAssetState_String pins the String() helper
// (fmt.Stringer). Each canonical value's String() returns
// the underlying string verbatim.
func TestAssetState_String(t *testing.T) {
	for _, s := range allAssetStates {
		t.Run(string(s), func(t *testing.T) {
			assert.Equal(t, string(s), s.String())
		})
	}
}

// TestAssetState_ZeroValueFromStateRejected pins the
// zero-value from-state guard. Verbatim the pipeline_state
// contract: an uninitialised AssetState MUST NOT pass any
// IsValidTransition check, including the (zero, zero)
// self-loop.
func TestAssetState_ZeroValueFromStateRejected(t *testing.T) {
	cases := []struct {
		name string
		s    AssetState
		to   AssetState
		want bool
	}{
		{"zero-self-loop-rejected", AssetState(""), AssetState(""), false},
		{"zero-to-canonical-rejected", AssetState(""), StateAssetDownloaded, false},
		{"zero-to-terminal-success-rejected", AssetState(""), StateAssetReadyMultilingual, false},
		{"zero-to-failure-rejected", AssetState(""), StateAssetFailedRetryable, false},
		{"zero-to-another-zero-rejected", AssetState(""), AssetState("UNKNOWN"), false},
		{"zero-to-lowercase-rejected", AssetState(""), AssetState("discovered"), false},
		// Sanity: a non-zero from-state with the same targets
		// behaves per the matrix.
		{"canonical-self-loop-allowed", StateAssetDiscovered, StateAssetDiscovered, true},
		{"canonical-to-canonical-allowed", StateAssetDiscovered, StateAssetDownloaded, true},
		{"canonical-to-unknown-rejected", StateAssetDiscovered, AssetState("UNKNOWN"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, c.s.IsValidTransition(c.to))
		})
	}
}

// TestAssetState_UnknownTargetRejected pins the unknown-
// target gate: IsValidTransition must reject transitions to
// ad-hoc values (the Valid() check on `to`).
func TestAssetState_UnknownTargetRejected(t *testing.T) {
	for _, to := range []AssetState{
		AssetState("UNKNOWN_STATE"),
		AssetState("discovered"),
		AssetState(""),
	} {
		t.Run(string(to), func(t *testing.T) {
			for _, from := range allAssetStates {
				got := from.IsValidTransition(to)
				if got {
					t.Errorf("IsValidTransition(%q, %q)=true; want false (unknown target must be rejected)", from, to)
				}
			}
		})
	}
}

// TestAssetState_CanonicalValuesExhaustive pins that
// CanonicalAssetStateValues returns all 14 values in the
// declared order. A future PR adding a 15th state must
// update the IsValidTransition matrix AND the helper-methods
// test AND the percheck_asset_state_canonical_14 archcheck.
func TestAssetState_CanonicalValuesExhaustive(t *testing.T) {
	assert.Len(t, CanonicalAssetStateValues(), 14,
		"CanonicalAssetStateValues must return exactly 14 values; if you add a 15th, update the matrix AND helper-methods test AND the percheck_asset_state_canonical_14 archcheck pin")
	assert.Equal(t, allAssetStates, CanonicalAssetStateValues(),
		"CanonicalAssetStateValues must return values in the same order as allAssetStates (test-side mirror)")
}

// TestAssetState_PreTerminalStatesLength pins that
// canonicalPreTerminalStates (production-side private) has
// exactly 11 entries — the 11 happy-path states. Per
// percheck_asset_state_canonical_14 the production slice
// must equal this test-side mirror exactly.
func TestAssetState_PreTerminalStatesLength(t *testing.T) {
	assert.Len(t, canonicalPreTerminalStates, 11,
		"canonicalPreTerminalStates must hold exactly 11 entries (the 11 happy-path states); update if you add/remove a happy-path state")
	assert.Equal(t, nonTerminalAssetStates, canonicalPreTerminalStates,
		"canonicalPreTerminalStates must mirror the test's nonTerminalAssetStates slice (godlike/06 SSOT — same shape across production+test)")
}
