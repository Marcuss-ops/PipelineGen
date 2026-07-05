// Package script — handler_legacy_int_stock_test.go. RETIRED by
// PR-script-legacy-contract (Jul 2026, P0 ABSOLUTE). The legacy
// /api/script/generate-from-clips endpoint no longer enqueues a
// script.generate job — it returns 410 Gone with the canonical
// deprecation payload pointing to POST /api/script/generate V2. The
// StockAssociationProcessor fallback contract is now exercised by
// the canonical V2 path (handler_generate_handler.go + the new
// StockBinding contract tests). The 410-Gone contract itself is
// pinned by TestLegacyGenerateFromClips_Returns410AndBody_PinContract
// + TestLegacyGenerateWithImages_Returns410AndBody_PinContract (new
// TDD tests in handler_legacy_deprecation_test.go).
//
// Pre-CR shape (preserved as audit-pin only): the test used
// pkg/veloxclient.SubmitAsync on the legacy endpoint with the Jackie
// Chan canned terminal result to verify StockAssociationProcessor
// fallback. Post-CR: the canonical /api/script/generate V2 path
// carries that contract — operator dashboards that alerted on the
// "StockBinding.Fallback" wire shape should pivot their probes to
// the V2 endpoint, not the retired /generate-from-clips. Forward-
// pointer: PR-V2-STOCK-FALLBACK-CONTRACT (2026-08-15) — wire a
// matching V2 integration test for the StockBinding contract.
package script

import "testing"

// TestLegacyGenerateFromClips_StockFallback_OnClipSource — RETIRED.
// See file header comment for the pointer to the new contract tests.
// Pre-CR shape: veloxclient.SubmitAsync call against the legacy
// /api/script/generate-from-clips endpoint asserted 200 OK + canned
// StockAssociationProcessor fallback shell. Post-CR the endpoint
// returns 410 Gone; the test is t.Skip()-ed with a clear pointer to
// the new canonical TDD tests below.
//
// godlike/07 no-fake-availability: a t.Skip with no justification is
// a foreground anti-pattern (operators' chrome tests would flake on
// "test mysteriously skipped"). The justification here is the file
// header + this docstring + the TDD contract-tests in
// handler_legacy_deprecation_test.go.
func TestLegacyGenerateFromClips_StockFallback_OnClipSource(t *testing.T) {
	t.Skip("PR-script-legacy-contract (Jul 2026, P0 ABSOLUTE): legacy /api/script/generate-from-clips endpoint retired to 410-Gone. StockAssociationProcessor fallback contract now exercised by canonical /api/script/generate V2. See TestLegacyGenerateFromClips_Returns410AndBody_PinContract in handler_legacy_deprecation_test.go for the new wire-shape contract test.")
}
