// Package asset — rights_state_test.go (PR-CLIPINGEST-PIPELINE
// Step 10, July 2026).
//
// Enum invariants test for RightsStatus (6) + ReviewStatus (4).
// Mirrors the pattern in asset_state_test.go (TestAssetState_*).
//
// godlike/06 SSOT (one canonical owner per fact): the test
// file at this path is the canonical TYPED test surface for
// rights_state.go's enums. A future agent modifying the
// enum alphabet MUST update BOTH:
//   - rights_state.go (the const declarations +
//     CanonicalXxxValues() getters)
//   - this test file (the closed-set value tests below)
//
// Drift between the 2 surfaces is caught by the
// percheck_rights_status_canonical_6 +
// percheck_review_status_canonical_4 archcheck gates.
package render

import (
	"strings"
	"testing"
)

// TestRightsStatus_CanonicalCount pins the count of canonical
// RightsStatus values to 6 (godlike/06 SSOT — change in the
// canonical count requires updating the migration file, the
// type surface, and this test in lockstep; the archcheck gate
// percheck_rights_status_canonical_6 catches drift).
func TestRightsStatus_CanonicalCount(t *testing.T) {
	got := len(CanonicalRightsStatusValues())
	if got != 6 {
		t.Fatalf("CanonicalRightsStatusValues count = %d, want 6 (godlike/06 SSOT — every alphabet update must extend the canonical surface AND its tests + migration CHECK in lockstep)", got)
	}
}

// TestRightsStatus_StringLiteralValues pins the EXACT wire
// alphabet: every value in CanonicalRightsStatusValues() must
// be one of the declared 6 consts in rights_state.go. This
// test catches alphabet-value drift at compile/run time; the
// archcheck gate percheck_rights_status_canonical_6 catches
// the COUNT drift at lint time. Together the two surfaces
// guarantee the canonical-6 invariant.
func TestRightsStatus_StringLiteralValues(t *testing.T) {
	want := map[string]struct{}{
		"owned":              {},
		"licensed":           {},
		"creative_commons":   {},
		"permission_granted": {},
		"review_required":    {},
		"blocked":            {},
	}
	for _, v := range CanonicalRightsStatusValues() {
		key := string(v)
		if _, ok := want[key]; !ok {
			t.Errorf("CanonicalRightsStatusValues() contains unknown wire value %q — alphabet drift between rights_state.go and this test", key)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Errorf("missing canonical RightsStatus values: %v (must add to rights_state.go's const block in lockstep)", sortedKeys(want))
	}
}

// TestRightsStatus_AllValid are the canonical 6 must all pass
// Valid() — guards against a future refactor that accidentally
// restricts the alphabet while not updating the count.
func TestRightsStatus_AllValid(t *testing.T) {
	for _, v := range CanonicalRightsStatusValues() {
		if !v.Valid() {
			t.Errorf("canonical RightsStatus %q failed Valid() — alphabet/canonical-surface drift", string(v))
		}
	}
}

// TestRightsStatus_NonCanonicalFailValid asserts that any
// ad-hoc string value fails Valid() (defensive against shadow
// declarations).
func TestRightsStatus_NonCanonicalFailValid(t *testing.T) {
	bad := []string{"OWNED", "Blocked", "review-required", "garbage", "x"}
	for _, raw := range bad {
		v := RightsStatus(raw)
		if v.Valid() {
			t.Errorf("shadow RightsStatus %q unexpectedly passed Valid() (drift between declared alphabet and runtime check)", raw)
		}
	}
}

// TestRightsStatus_PublishableSplit pins the IsPublishable
// predicate: 4 publishable + 2 restricted. The split matches
// the user-spec "Clip Pre-Planner ignora automaticamente asset
// blocked o review_required" rule.
func TestRightsStatus_PublishableSplit(t *testing.T) {
	publishable := map[string]bool{
		"owned":              true,
		"licensed":           true,
		"creative_commons":   true,
		"permission_granted": true,
		"review_required":    false,
		"blocked":            false,
	}
	for _, v := range CanonicalRightsStatusValues() {
		want, ok := publishable[string(v)]
		if !ok {
			t.Errorf("test fixture missing canonical value %q (drift between canonical surface and PublishableSplit test)", string(v))
			continue
		}
		if got := v.IsPublishable(); got != want {
			t.Errorf("RightsStatus(%q).IsPublishable() = %v, want %v", string(v), got, want)
		}
	}
}

// TestRightsStatus_RestrictedPredicate asserts the canonical
// "must skip" set the planner filter iterates. The set MUST
// equal exactly {review_required, blocked}; the contract is
// "the planner auto-skips these two values".
func TestRightsStatus_RestrictedPredicate(t *testing.T) {
	got := RestrictedRightsStatuses()
	want := map[string]struct{}{"review_required": {}, "blocked": {}}
	for _, v := range got {
		if _, ok := want[string(v)]; !ok {
			t.Errorf("IsRightsRestrictedPredicate returned unexpected value %q — drift in canonical skip set", string(v))
		}
		delete(want, string(v))
	}
	if len(want) != 0 {
		t.Errorf("IsRightsRestrictedPredicate missing canonical values: %v", sortedKeys(want))
	}
}

// TestRightsStatus_ZeroValueFailClosed asserts that the zero-
// value RightsStatus("") fails IsPublishable (godlike/07
// fail-closed: a row with a NULL/empty rights_status fails
// the planner filter by default, NOT pass-through).
func TestRightsStatus_ZeroValueFailClosed(t *testing.T) {
	var z RightsStatus
	if z.IsPublishable() {
		t.Errorf("zero-value RightsStatus should fail IsPublishable (godlike/07 fail-closed at runtime boundary)")
	}
	if z.Valid() {
		t.Errorf("zero-value RightsStatus should fail Valid()")
	}
}

// ── ReviewStatus tests (mirror surface) ───────────────────────────

// TestReviewStatus_CanonicalCount pins the count of canonical
// ReviewStatus values to 4.
func TestReviewStatus_CanonicalCount(t *testing.T) {
	got := len(CanonicalReviewStatusValues())
	if got != 4 {
		t.Fatalf("CanonicalReviewStatusValues count = %d, want 4 (godlike/06 SSOT — alphabet update requires canonical surface + tests + migration CHECK in lockstep)", got)
	}
}

// TestReviewStatus_StringLiteralValues pins the EXACT wire
// alphabet: must be {none, pending, approved, rejected}.
func TestReviewStatus_StringLiteralValues(t *testing.T) {
	want := map[string]struct{}{
		"none":     {},
		"pending":  {},
		"approved": {},
		"rejected": {},
	}
	for _, v := range CanonicalReviewStatusValues() {
		key := string(v)
		if _, ok := want[key]; !ok {
			t.Errorf("CanonicalReviewStatusValues() contains unknown wire value %q — alphabet drift between rights_state.go and this test", key)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Errorf("missing canonical ReviewStatus values: %v", sortedKeys(want))
	}
}

// TestReviewStatus_NonCanonicalFailValid asserts shadow
// declarations fail Valid().
func TestReviewStatus_NonCanonicalFailValid(t *testing.T) {
	bad := []string{"None", "PENDING", "garbage", "x"}
	for _, raw := range bad {
		v := ReviewStatus(raw)
		if v.Valid() {
			t.Errorf("shadow ReviewStatus %q unexpectedly passed Valid()", raw)
		}
	}
}

// TestReviewStatus_GateSplit asserts IsReviewGateRequired
// returns true ONLY for pending + rejected. None + Approved
// are planner-pass values.
func TestReviewStatus_GateSplit(t *testing.T) {
	gated := map[string]bool{
		"none":     false,
		"pending":  true,
		"approved": false,
		"rejected": true,
	}
	for _, v := range CanonicalReviewStatusValues() {
		want, ok := gated[string(v)]
		if !ok {
			t.Errorf("test fixture missing canonical value %q (drift between canonical surface and GateSplit test)", string(v))
			continue
		}
		if got := v.IsReviewGateRequired(); got != want {
			t.Errorf("ReviewStatus(%q).IsReviewGateRequired() = %v, want %v", string(v), got, want)
		}
	}
}

// TestReviewStatus_ZeroValueOpenByDefault asserts the zero-
// value ReviewStatus("") is the fail-OPEN choice for the review
// dimension (mirrors the migration DEFAULT 'none'). The
// godlike/07 fail-closed contract is on the RIGHTS surface
// (which IS publishable=false on zero); the review surface is
// a SECONDARY gate that DOES NOT spuriously block on missing
// data (legacy pre-Step-10 rows have NULL review_status).
func TestReviewStatus_ZeroValueOpenByDefault(t *testing.T) {
	var z ReviewStatus
	if z.IsReviewGateRequired() {
		t.Errorf("zero-value ReviewStatus should be open by default (fail-open on review dimension; RightsStatus is the authoritative surface, not ReviewStatus)")
	}
}

// ── Default constants smoke test (mirrors asset_state_test.go) ──

// TestRightsStatus_DefaultConstants pins the migration 158
// DEFAULT literals for the two enum columns. Drift between
// the test and the const declarations breaks the migration's
// pre-conditions and is caught at test time.
func TestRightsStatus_DefaultConstants(t *testing.T) {
	if DefaultRightsStatus != RightsStatusReviewRequired {
		t.Errorf("DefaultRightsStatus = %q, want %q (must remain aligned with canonical.go + migration 158 DEFAULT for legacy rows)", DefaultRightsStatus, RightsStatusReviewRequired)
	}
	if DefaultReviewStatus != ReviewStatusNone {
		t.Errorf("DefaultReviewStatus = %q, want %q (must remain aligned with canonical.go + migration 158 DEFAULT for the review_status column)", DefaultReviewStatus, ReviewStatusNone)
	}
}

// ── Helper: deterministic test-failure output ─────────────────────

// sortedKeys returns the keys of m in lexicographic order so a
// failing test message lists them in a stable order (operator-
// friendly diff).
func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Tiny alphabetical sort without sort package import.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && strings.Compare(out[j-1], out[j]) > 0; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
