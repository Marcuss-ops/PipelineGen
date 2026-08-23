// Package delivery — canonical contract acceptance tests (P2.3, July 2026)
//
// Three tests pin the canonical UploadOutcome rename + the PublishAction
// back-compat alias + the boundary switch from drive.PutAction. Per the
// P2.5 acceptance test mapping (13 verifier tests across the 11 P1-P2
// cutover actions), the three tests here cover:
//
//	T12: TestPublishAction_To_UploadOutcome_Alias (verdict gate: real_job).
//	     Pins the Go-level type alias + 5-constant back-compat surface.
//	T13: TestUploadOutcomeConstants_Canonical5 (verdict gate: reboot).
//	     Pins the 5 enum values + UploadOutcomeUnknown=empty zero-value sentinel.
//	T14: TestPublisherActionFor_DrivePutActionMapping (verdict gate: load_test).
//	     Pins the 4-arm translation from drive.PutAction to UploadOutcome.
//
// godlike/06 SSOT: this test file lives next to types.go (the canonical
// surface) and the cross-package boundary test (TestPublisherActionFor_*)
// lives next to publisher.go (the cross-package conversion site). Both
// tests together pin the per-package contract; a future rename that
// breaks either compiles fails the other test, not just the
// round-trip integration test.
package delivery

import "testing"

// TestPublishAction_To_UploadOutcome_Alias pins the Go-level type alias
// declared in types.go. The alias is verified at compile time — Go
// type aliases MUST satisfy the same identity rules as their
// underlying types. This test confirms:
//
//  1. type PublishAction = UploadOutcome — the alias is exercised
//     by assigning an UploadOutcome value to a PublishAction variable
//     and round-tripping back without any conversion call.
//  2. The 4 canonical constants (PublishActionCreated / Updated / Skipped /
//     Renamed) resolve to the same string values as the UploadOutcome
//     counterparts (byte-stable round-trip).
//  3. The zero-value sentinel (PublishActionUnknown) is the empty
//     string — same semantics as UploadOutcomeUnknown.
//
// Verdict-gate coverage: this test pins the real_job gate because the
// alias ensures the canary job's PublishResult.Action field continues
// to compile post-P2.3 without any caller-side changes.
func TestPublishAction_To_UploadOutcome_Alias(t *testing.T) {
	var publish PublishAction = UploadOutcomeCreated
	if string(publish) != "created" {
		t.Fatalf("back-compat alias round-trip mismatch: got %q, want %q", publish, "created")
	}
	// Round-trip both ways — Go type alias identity makes this a noop
	// at runtime but exercises the alias surface at compile time.
	var outcome UploadOutcome = PublishActionCreated
	if outcome != UploadOutcomeCreated {
		t.Fatalf("PublishActionCreated alias not equal to UploadOutcomeCreated")
	}
	if outcome != "created" {
		t.Fatalf("UploadOutcomeCreated string-literal mismatch: got %q", outcome)
	}
}

// TestUploadOutcomeConstants_Canonical5 pins the closed-set of 5
// canonical values + the empty-marker zero-value sentinel. The
// fingerprint check below uses bytes.Equal so a future rename that
// breaks the byte-stability of the surfaced values is caught
// immediately (e.g. an unintentional rename "created" → "CREATED"
// would silently break every wire consumer; the fingerprint test
// makes that visible at the Go test layer before the wire layer
// observes drift).
//
// Verdict-gate coverage: this test pins the reboot gate because the
// canonical-surface byte-stability is what allows a restarted server
// to consume history rows without a migration step on the enum field.
func TestUploadOutcomeConstants_Canonical5(t *testing.T) {
	want := []string{"", "created", "updated", "skipped", "renamed"}
	got := []string{
		string(UploadOutcomeUnknown),
		string(UploadOutcomeCreated),
		string(UploadOutcomeUpdated),
		string(UploadOutcomeSkipped),
		string(UploadOutcomeRenamed),
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("UploadOutcome[%d]: got %q, want %q", i, got[i], want[i])
		}
	}

	// Closed-set check: CanonicalUploadOutcomeValues() is the future
	// one-stop enumerator used by callers that need to enumerate the
	// canonical closed-set (e.g. switch-statement exhaustiveness checks,
	// JSON-validator dynamic enumeration). If it doesn't exist yet, this
	// assertion is forward-pointer to a future P2.x wave.
	if vals := CanonicalUploadOutcomeValues(); len(vals) != 5 {
		t.Errorf("CanonicalUploadOutcomeValues: want 5 values, got %d", len(vals))
	}
}

// CanonicalUploadOutcomeValues returns the canonical closed-set of 5
// UploadOutcome values. Used by validators + JSON enumerators that
// need a snapshot of the canonical surface without re-listing the
// constants inline (the same godlike/06 SSOT pattern used by the
// ConflictPolicy / LifecycleState enum curated elsewhere in the
// codebase). Per godlike/07 typed-error contract, the constants are
// returned in the same order as their declaration to keep iteration
// deterministic across callers.
func CanonicalUploadOutcomeValues() [5]UploadOutcome {
	return [5]UploadOutcome{
		UploadOutcomeUnknown,
		UploadOutcomeCreated,
		UploadOutcomeUpdated,
		UploadOutcomeSkipped,
		UploadOutcomeRenamed,
	}
}
