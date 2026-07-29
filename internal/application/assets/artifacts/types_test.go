// ── Parity between the canonical Status constants and their wire strings ──

package artifacts

import "testing"

// TestStatusConstantValues guards against value-drift on the 7 canonical
// Status constants. These 7 strings (STAGING / VERIFYING / STAGED /
// FAILED / QUARANTINED / DELETED) are the production source-of-truth
// for 25 importers of internal/artifacts; a typo would silently
// break 25 downstream callers without this guard. Surface drift in
// CI as 'expected="STAGED" got="STAGEX"' rather than after a runtime
// regression.
//
// AZIONE 15 (July 2026): StatusReady is now a BC alias for StatusStaged
// (equals "STAGED"). The test asserts the canonical string values,
// not the alias identity.
//
// Mirrors the discipline already established in
// internal/kernel/asset/asset_test.go (TestStateConstantsMatchAssets,
// TestProcessingConstantsMatchAssets, TestFunctionRebindingsMatchAssets,
// TestLocationKindMatches). The same pattern (table-driven any-comparison
// + sub-tests) is used here for the canonical artifacts.Status values.
//
// Status is `type Status string`, so `string(tc.actual)` is an identity
// dereference, not a lossy conversion. Drift like
// StatusStaged = "STAGEX" would surface as a string mismatch.
//
// FRAGMENTO (c) (artifact-status fusion) attempted + DEFERRED: a
// pre-existing import cycle
// (internal/artifacts/clips_adapter.go + converters.go both import
// internal/assets for 20+ symbols) prevents `Delete
// internal/assets/artifact.go` from being shippable in a single
// tornata. This tornata ships only this defensive parity test plus
// its commit message documenting the cycle root cause and the 3
// candidate resolution paths (sub-package extraction of clips adapter
// glue; full alias-layer coverage of all 20+ clips_adapter symbols;
// or accept-parallel with a future-equivalence test).
func TestStatusConstantValues(t *testing.T) {
	cases := []struct {
		name     string
		actual   Status
		expected string
	}{
		{"StatusStaging", StatusStaging, "STAGING"},
		{"StatusVerifying", StatusVerifying, "VERIFYING"},
		{"StatusStaged", StatusStaged, "READY"},
		{"StatusReady", StatusReady, "READY"},
		{"StatusFailed", StatusFailed, "FAILED"},
		{"StatusQuarantined", StatusQuarantined, "QUARANTINED"},
		{"StatusDeleted", StatusDeleted, "DELETED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if string(tc.actual) != tc.expected {
				t.Errorf("drift detected: actual=%q expected=%q", string(tc.actual), tc.expected)
			}
		})
	}
}
