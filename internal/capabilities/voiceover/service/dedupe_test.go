// Package voiceover — dedupe_test.go (Step 7/12, June 2026).
//
// Table-driven unit tests for the canonical PR-VO-B3 dedupe gate
// verdict projection (DecideDedupe + DedupeDecision.String). The
// gate is the Stage-3 finalize behavior surface that the user pinned:
// "1 match → DedupeReuse, >1 match → DedupeConflict". These tests
// pin the boundary so a future refactor cannot silently change the
// decision semantics without breaking the unit contract.
//
// Scope discipline: the tests cover ONLY the pure-function helper
// (no I/O, no repo stub, no finalizeStage integration). The
// finalizeStage integration is covered by service_p01_state_machine_test.go
// via stubRepo.CountByDriveFileIDTx; the gate's switch on
// DecideDedupe is exercised by the stubRepo harness path which keeps
// finalizeStage's existing test surface green. The pure helper tests
// here are the fast / hermetic boundary contract.
package voiceover

import "testing"

// TestDecideDedupe is the canonical projection contract test
// (Step 7/12). Each row pins ONE count→decision boundary so a
// regression cannot silently shift the boundary without breaking
// the table.
func TestDecideDedupe(t *testing.T) {
	tests := []struct {
		name  string
		count int
		want  DedupeDecision
	}{
		// 0 matches → gate is a no-op (fall through to atomic-swap
		// DELETE+INSERT+Outbox+Commit). This is the most common
		// case in production (fresh audio, no prior row).
		{"zero_matches_continue", 0, DedupeContinue},

		// 1 match → REUSE the matched (canonical) row, skip the
		// INSERT. The user's primary spec: "1 match → DedupeReuse
		// (riusa il record, NON creare duplicato)".
		{"one_match_reuse", 1, DedupeReuse},

		// >1 matches → fail-closed per godlike/07's no fake
		// availability policy. The dedupe gate's invariant (one
		// canonical row per DriveFileID) is broken; do NOT insert
		// a duplicate row, surface FailureDedupeAmbiguous + WARN.
		// {"two_matches_conflict", 2, DedupeConflict},
		// {"three_matches_conflict", 3, DedupeConflict},
		// {"many_matches_conflict", 100, DedupeConflict},
		{"two_matches_conflict", 2, DedupeConflict},
		{"three_matches_conflict", 3, DedupeConflict},
		{"large_count_conflict", 100, DedupeConflict},

		// Negative counts: defensive bounce to DedupeContinue. The
		// CountByDriveFileIDTx port is contract-bounded to count>=0,
		// but the bounded-regression profile requires that any
		// unspecified value treats as continue so the gate never
		// blocks production traffic on a malformed count.
		{"negative_one_continue", -1, DedupeContinue},
		{"negative_large_continue", -100, DedupeContinue},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DecideDedupe(tt.count); got != tt.want {
				t.Errorf("DecideDedupe(%d) = %q, want %q", tt.count, got, tt.want)
			}
		})
	}
}

// TestDedupeDecision_String pins the fmt.Stringer contract so the
// decision logs cleanly via zap.String without manual conversion
// (the canonical zap-key for operator audit trails). The values
// match the canonical wire strings ("continue", "reuse", "conflict")
// declared in dedupe.go so a future rename cannot silently break
// audit-log pipelines that grep for these specific strings.
func TestDedupeDecision_String(t *testing.T) {
	tests := []struct {
		name string
		d    DedupeDecision
		want string
	}{
		{"continue", DedupeContinue, "continue"},
		{"reuse", DedupeReuse, "reuse"},
		{"conflict", DedupeConflict, "conflict"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.String(); got != tt.want {
				t.Errorf("DedupeDecision(%s).String() = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

// TestDecideDedupe_IsExhaustive pins the boundary property that
// the function returns exactly one of the three canonical values
// for ANY input. This guards against a future contributor adding
// a divergent code path that returns a custom string (a regression
// that would silently bypass the typed decision switch in
// finalizeStage at runtime even though it compiles).
func TestDecideDedupe_IsExhaustive(t *testing.T) {
	canonicalValues := map[DedupeDecision]bool{
		DedupeContinue: true,
		DedupeReuse:    true,
		DedupeConflict: true,
	}
	// Sweep boundaries + a few interior values to make sure every
	// count in the sweep produces a canonical value (no off-the-
	// shelf DedupeDecision literals returning).
	for _, c := range []int{-100, -1, 0, 1, 2, 5, 50} {
		got := DecideDedupe(c)
		if !canonicalValues[got] {
			t.Errorf("DecideDedupe(%d) returned %q which is not one of the three canonical values (continue/reuse/conflict)", c, got)
		}
	}
}
