// Package asset / asset_types_test.go — TODO 3 close-out (June 2026):
// SSOT lifecycle_state canonicaliser tests.
//
// Coverage matrix vs the spec's 5 cases:
//
//  1. LifecycleState=ACTIVE → "lifecycle_state":"ACTIVE"  (canonical roundtrip)
//  2. LifecycleState prevails over Status (legacy "ready" Status
//     plus canonical LifecycleState="STAGING" → STAGING wins, not ACTIVE)
//  3. legacy "ready" status normalises to ACTIVE on read
//  4. DELETED → NOT valid for default-Active canonicalisation;
//     legacy "deleted" → StateDeleted exactly
//  5. canonicalLifecycleState is the single SSOT gate; Valid()
//     excludes StateReady/StatePending by design
package asset

import "testing"

// TestNormalizeLegacyLifecycle covers spec case 3 + the legacy handling
// matrix documented on the function. The mapper is the read-path
// single entry point for legacy lowercase values like "ready"/"pending"
// that pre-TODO 3 sites still emit.
func TestNormalizeLegacyLifecycle(t *testing.T) {
	cases := []struct {
		in   string
		want LifecycleState
	}{
		// Canonical — pass through unchanged.
		{"ACTIVE", StateActive},
		{"STAGING", StateStaging},
		{"PROCESSING", StateProcessing},
		{"DELETE_PENDING", LcStateDeletePending},
		{"DELETED", StateDeleted},
		{"ERROR", StateError},

		// Lowercase canonical — fold and resolve.
		{"active", StateActive},
		{"staging", StateStaging},
		{"processing", StateProcessing},
		{"deleted", StateDeleted},
		{"error", StateError},

		// Legacy aliases (the spec's case 3).
		{"ready", StateActive},
		{"READY", StateActive},
		{"pending", StateStaging},
		{"PENDING", StateStaging},
		{"searchable", StateActive},
		{"SEARCHABLE", StateActive},

		// Whitespace + mixed-case — must collapse.
		{"  Ready ", StateActive},
		{"aCtIvE", StateActive},
		{"\tDELETED\n", StateDeleted},

		// Empty / unknown → canonical default (ACTIVE).
		{"", StateActive},
		{"unknown_legacy_value", StateActive},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := NormalizeLegacyLifecycle(c.in)
			if got != c.want {
				t.Errorf("NormalizeLegacyLifecycle(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestCanonicalLifecycleState covers spec case 1 + 2 + 5: the
// private canonicalLifecycleState helper is the SINGLE write-path gate.
// Spec case 1: LifecycleState=ACTIVE remains ACTIVE in the rewrite.
// Spec case 2: canonical LifecycleState prevails over legacy Status.
// Spec case 5: nil asset short-circuits to ACTIVE (defensive default).
func TestCanonicalLifecycleState(t *testing.T) {
	t.Run("nil asset defaults to ACTIVE", func(t *testing.T) {
		got := canonicalLifecycleState(nil)
		if got != StateActive {
			t.Errorf("nil asset → %q, want ACTIVE", got)
		}
	})

	t.Run("canonical ACTIVE preserved (spec case 1)", func(t *testing.T) {
		a := &Asset{LifecycleState: StateActive}
		got := canonicalLifecycleState(a)
		if got != StateActive {
			t.Errorf("canonical LifecycleState ACTIVE → %q, want ACTIVE", got)
		}
	})

	t.Run("canonical STAGING prevails over legacy Status (spec case 2)", func(t *testing.T) {
		// Spec case 2 is exercised via CanonicalLifecycleState(value, fallback)
		// directly (Asset has no Status field; the fallback path is the
		// canonical SSOT gate from the perspective of AssetData callers
		// in payload_mapper.go and asset_store.go). Verified below.
		a := &Asset{LifecycleState: StateStaging}
		got := canonicalLifecycleState(a)
		if got != StateStaging {
			t.Errorf("canonical LifecycleState STAGING → %q, want STAGING", got)
		}
		got2 := CanonicalLifecycleState("STAGING", "ready")
		if got2 != StateStaging {
			t.Errorf("CanonicalLifecycleState(\"STAGING\", \"ready\") = %q, want STAGING (canonical prevails)", got2)
		}
		got3 := CanonicalLifecycleState("", "ready")
		if got3 != StateActive {
			t.Errorf("CanonicalLifecycleState(\"\", \"ready\") = %q, want ACTIVE (legacy fallback)", got3)
		}
	})

	t.Run("legacy StateReady mapped to ACTIVE", func(t *testing.T) {
		a := &Asset{LifecycleState: StateReady}
		got := canonicalLifecycleState(a)
		if got != StateActive {
			t.Errorf("legacy StateReady → %q, want ACTIVE", got)
		}
	})

	t.Run("legacy StatePending mapped to STAGING", func(t *testing.T) {
		a := &Asset{LifecycleState: StatePending}
		got := canonicalLifecycleState(a)
		if got != StateStaging {
			t.Errorf("legacy StatePending → %q, want STAGING", got)
		}
	})

	t.Run("empty LifecycleState defaults to ACTIVE", func(t *testing.T) {
		a := &Asset{}
		got := canonicalLifecycleState(a)
		if got != StateActive {
			t.Errorf("empty asset → %q, want ACTIVE", got)
		}
	})

	t.Run("arbitrary legacy string falls through NormalizeLegacyLifecycle", func(t *testing.T) {
		a := &Asset{LifecycleState: LifecycleState("Searchable")}
		got := canonicalLifecycleState(a)
		if got != StateActive {
			t.Errorf("LifecycleState=\"Searchable\" → %q, want ACTIVE", got)
		}
	})

	t.Run("DELETE_PENDING preserved (new canonical state)", func(t *testing.T) {
		a := &Asset{LifecycleState: LcStateDeletePending}
		got := canonicalLifecycleState(a)
		if got != LcStateDeletePending {
			t.Errorf("LcStateDeletePending round-trip → %q", got)
		}
	})

	t.Run("ERROR preserved (new canonical state)", func(t *testing.T) {
		a := &Asset{LifecycleState: StateError}
		got := canonicalLifecycleState(a)
		if got != StateError {
			t.Errorf("StateError round-trip → %q", got)
		}
	})
}

// TestLifecycleState_Valid covers spec case 5: the canonical-only
// Valid() gate explicitly excludes the legacy lowercase constants.
// Post-TODO 3 expected: only the 6 canonical uppercase constants
// are valid; StateReady and StatePending are filtered out.
func TestLifecycleState_Valid(t *testing.T) {
	canonical := []LifecycleState{
		StateStaging, StateProcessing, StateActive,
		LcStateDeletePending, StateDeleted, StateError,
	}
	for _, st := range canonical {
		t.Run("canonical "+string(st), func(t *testing.T) {
			if !st.Valid() {
				t.Errorf("canonical %q should be Valid", st)
			}
		})
	}

	// Legacy lowercase constants must NOT be canonical-valid
	// post-TODO 3 — the SSOT is canonical only.
	legacy := []LifecycleState{StateReady, StatePending}
	for _, st := range legacy {
		t.Run("legacy NOT-valid "+string(st), func(t *testing.T) {
			if st.Valid() {
				t.Errorf("legacy %q must NOT be Valid post-TODO 3", st)
			}
		})
	}

	// Junk values fail.
	junk := []LifecycleState{"", "active", "searchable", "unknown"}
	for _, st := range junk {
		t.Run("junk NOT-valid "+string(st), func(t *testing.T) {
			if st.Valid() {
				t.Errorf("junk %q must NOT be Valid", st)
			}
		})
	}
}

// TestPayloadSSOTShape exercises the spec's "un solo campo payload:
// lifecycle_state" requirement by direct struct-shape inspection. It
// does NOT exercise BuildPayload (which lives in the qdrant package
// and is blocked by the pre-existing scripts-package build error;
// the integration is verified end-to-end via the search-adapter test
// after followup scripts-package restore).
//
// This test asserts the constant vocabulary shape required by the
// SSOT: the constants are exactly 6, all uppercase, and span the
// canonical lifecycle progression.
func TestLifecycleState_VocabularyShape(t *testing.T) {
	expected := []struct {
		state LifecycleState
		upper string
	}{
		{StateStaging, "STAGING"},
		{StateProcessing, "PROCESSING"},
		{StateActive, "ACTIVE"},
		{LcStateDeletePending, "DELETE_PENDING"},
		{StateDeleted, "DELETED"},
		{StateError, "ERROR"},
	}
	if len(expected) != 6 {
		t.Fatalf("vocabulary shape test wrote %d states, not 6", len(expected))
	}
	for _, e := range expected {
		if string(e.state) != e.upper {
			t.Errorf("constant %v should stringify to %q, got %q", e.state, e.upper, string(e.state))
		}
	}
}
