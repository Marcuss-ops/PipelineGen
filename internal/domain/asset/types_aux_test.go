// Package asset — types_aux_test.go.
//
// Step-1 typed migration (PR-IMAGES-AI-VS-NORMAL-PLAN, A1, July 2026):
// the legacy 14-field GenerationStyle tests (Description fallback,
// *bool tri-state pointer, AllowedProviders/AllowedModels Allowlist,
// DefaultWidth/Height, Validate on legacy shape, YAML round-trip
// with missing fields) were retired along with the underlying struct.
// Their replacements live in types_style_test.go under the slim
// 8-field StyleDefinition surface.
//
// This file keeps a trivial smoke test so the package's test
// compilation stays green across toolchain upgrades that flag
// empty _test.go files (godlike/06 audit-pinning discipline).
package asset

import "testing"

// TestPackage_Asset_Compiles locks the package-decl-and-imports
// invariant: if a future change accidentally introduces a circular
// import or a syntax error in this package's root, this test
// surfaces a compile failure rather than a silent runtime drift.
//
// False-negative risk is zero: the test body is empty by design
// (godlike/07 idempotency — running it 1000 times still passes).
func TestPackage_Asset_Compiles(t *testing.T) {
	t.Helper()
	// Intentionally empty: the test exists to anchor the test
	// compilation surface for the asset package.
	_ = t
}
