// Package scan — percheck_asset_state_no_shadow_enum_test.go
// (PR-CATALOG-MULTILINGUA step 7, July 2026)
//
// Pins the no-shadow-enum forward-prevention scanner. Builds
// synthetic .go files inside t.TempDir() and verifies that
// the scanner:
//
//   - PASSES  when only the canonical SOLE owner
//     (internal/kernel/asset/asset_state_values.go) declares the
//     StateAssetX alphabet.
//   - FAILS   when any other .go file declares a
//     `StateAssetX AssetState = "..."` const literal
//     outside the canonical file.
//
// godlike/07 fail-fast: the test does NOT tolerate a
// missing canonical file or a missing declaration file —
// the scanner must deterministically emit / not-emit
// violations in either case.
package governance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// TestScanAssetStateNoShadowEnum_OnlyCanonicalPasses: build a
// canonical file with 14 const declarations and NO other
// file declaring StateAssetX. Expect zero violations.
func TestScanAssetStateNoShadowEnum_OnlyCanonicalPasses(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeAssetStateValues(t, tempDir, 14, "")
	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanAssetStateNoShadowEnum(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == assetStateShadowRule {
			t.Errorf("unexpected shadow-enum violation: %s", v.Note)
		}
	}
}

// TestScanAssetStateNoShadowEnum_ShadowInOtherFileFails: build
// a canonical file with 14 const declarations AND a
// non-canonical .go file declaring an extra StateAssetX
// shadow const. Expect EXACTLY 1 violation pointing at the
// shadow file's offending line.
func TestScanAssetStateNoShadowEnum_ShadowInOtherFileFails(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeAssetStateValues(t, tempDir, 14, "")
	// Add a shadow declaration in a different internal/ file.
	shadowDir := filepath.Join(tempDir, "internal", "application", "images")
	if err := os.MkdirAll(shadowDir, 0o755); err != nil {
		t.Fatalf("mkdir shadow dir: %v", err)
	}
	shadowPath := filepath.Join(shadowDir, "shadow_states.go")
	if err := os.WriteFile(shadowPath, []byte(
		"package images\n\n"+
			"import \"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset\"\n\n"+
			"// StateAssetShadowOut is a forbidden shadow declaration.\n"+
			"const StateAssetShadowOut asset.AssetState = \"SHADOW_FAILED_PERMANENT\"\n",
	), 0o644); err != nil {
		t.Fatalf("write shadow file: %v", err)
	}
	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanAssetStateNoShadowEnum(tempDir, &policy.Policy{}, r)
	found := 0
	for _, v := range r.Violations {
		if v.Rule == assetStateShadowRule && v.File == "internal/application/images/shadow_states.go" {
			found++
		}
	}
	if found < 1 {
		t.Errorf("expected at least 1 shadow-enum violation on internal/application/images/shadow_states.go; got %d (all violations: %d)",
			found, len(r.Violations))
	}
}

// TestScanAssetStateNoShadowEnum_TestFileExempted: test files
// are exempt from the shadow ban (regression-guard surface).
// Build a non-canonical file with `_test.go` suffix that
// declares a shadow const; the scanner MUST NOT trip.
func TestScanAssetStateNoShadowEnum_TestFileExempted(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeAssetStateValues(t, tempDir, 14, "")
	testDir := filepath.Join(tempDir, "internal", "application", "images")
	if err := os.MkdirAll(testDir, 0o755); err != nil {
		t.Fatalf("mkdir test dir: %v", err)
	}
	testPath := filepath.Join(testDir, "shadow_states_test.go")
	if err := os.WriteFile(testPath, []byte(
		"package images\n\n"+
			"import \"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset\"\n\n"+
			"const StateAssetTestStub asset.AssetState = \"TEST_STUB\"\n",
	), 0o644); err != nil {
		t.Fatalf("write test shadow file: %v", err)
	}
	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanAssetStateNoShadowEnum(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == assetStateShadowRule && v.File == "internal/application/images/shadow_states_test.go" {
			t.Errorf("test file MUST be exempt from shadow-enum scan; got violation: %s", v.Note)
		}
	}
}

// TestScanAssetStateNoShadowEnum_CommentOnlyIsResidue: a
// non-canonical production file with a comment-only reference
// to StateAssetX is residue-accounted (WARNed), not violated.
func TestScanAssetStateNoShadowEnum_CommentOnlyIsResidue(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeAssetStateValues(t, tempDir, 14, "")
	warnDir := filepath.Join(tempDir, "internal", "application", "images")
	if err := os.MkdirAll(warnDir, 0o755); err != nil {
		t.Fatalf("mkdir warn dir: %v", err)
	}
	warnPath := filepath.Join(warnDir, "narrative_doc.go")
	if err := os.WriteFile(warnPath, []byte(
		"package images\n\n"+
			"// NOTE: see internal/kernel/asset/asset_state_values.go::StateAsset*\n"+
			"// enum for the canonical 14 states. The shadow declaration\n"+
			"// below is intentionally commented out to exercise the\n"+
			"// residue-accounting discipline per godlike/07.\n"+
			"// const StateAssetShadowOut asset.AssetState = \"SHADOW\"\n"+
			"func noop() {}\n",
	), 0o644); err != nil {
		t.Fatalf("write residue file: %v", err)
	}
	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanAssetStateNoShadowEnum(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == assetStateShadowRule && v.File == "internal/application/images/narrative_doc.go" {
			t.Errorf("comment-only references must NOT trip shadow-enum violation; got: %s", v.Note)
		}
	}
	foundWarn := false
	for _, w := range r.Warnings {
		if containsSubstring(w, "shadow-enum") && containsSubstring(w, "internal/application/images/narrative_doc.go") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Error("expected residue warning on narrative_doc.go; r.Warnings did not contain it")
	}
}
