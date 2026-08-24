// Package scan — percheck_asset_state_canonical_14_test.go
// (PR-CATALOG-MULTILINGUA step 7, July 2026)
//
// Pins the canonical-14-count forward-prevention scanner.
// Builds a synthetic asset_state_values.go inside a t.TempDir() and
// verifies that the scanner:
//
//   - PASSES  when the canonical file declares exactly 14
//     StateAssetX AssetState = "..." constants.
//   - FAILS   when the count drifts above OR below 14 (a
//     future agent who silently adds/removes a constant
//     surfaces as a CI build failure, not an operator trap).
//
// godlike/07 fail-fast: the test does NOT tolerate a missing
// canonical file — the scanner emits a violation in that
// case too (mirrors percheck_image_asset_invariants_test.go's
// canonical-owner-missing policy).
package boundaries

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeFakeAssetState creates a tempDir/internal/kernel/asset/
// asset_state_values.go file with exactly `count` StateAssetX
// const declarations of the canonical literal shape. The
// rest of the file is a minimal scaffolding the scanner
// expects to find (package decl). Returns the absolute path
// of the canonical file.
func writeFakeAssetStateValues(t *testing.T, tempDir string, count int, prefix string) string {
	t.Helper()
	dir := filepath.Join(tempDir, "internal", "kernel", "asset")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fake asset_state dir: %v", err)
	}
	var b []byte
	b = append(b, []byte("package asset\n\n")...)
	b = append(b, []byte("type AssetState string\n\n")...)
	b = append(b, []byte("const (\n")...)
	for i := 0; i < count; i++ {
		// Generate a unique stub identifier so the regex
		// matches each one. Stubs are intentionally
		// non-canonical values; the scanner trips on the
		// shape, not the value.
		b = append(b, []byte("\tStateAssetStub"+prefix+string(rune('A'+i))+
			" AssetState = \"STUB\"\n")...)
	}
	b = append(b, []byte(")\n")...)
	path := filepath.Join(dir, "asset_state_values.go")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write fake asset_state_values.go: %v", err)
	}
	return path
}

// TestScanAssetStateCanonical14_ExactlyFourteen verifies the
// happy path: 14 const declarations should NOT trip any
// violation. Comment-only references are residue, not
// violations.
func TestScanAssetStateCanonical14_ExactlyFourteen(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeAssetStateValues(t, tempDir, 14, "")
	// Add a comment-only reference to confirm residue accounting.
	if err := os.WriteFile(
		filepath.Join(tempDir, assetStateCanonical14Path),
		[]byte("package asset\n\n"+
			"// NOTE: 14-state machine — see StateAsset* for the canonical list.\n"+
			"type AssetState string\n\n"+
			"const (\n"+
			"\tStateAssetA AssetState = \"A\"\n"+
			"\tStateAssetB AssetState = \"B\"\n"+
			"\tStateAssetC AssetState = \"C\"\n"+
			"\tStateAssetD AssetState = \"D\"\n"+
			"\tStateAssetE AssetState = \"E\"\n"+
			"\tStateAssetF AssetState = \"F\"\n"+
			"\tStateAssetG AssetState = \"G\"\n"+
			"\tStateAssetH AssetState = \"H\"\n"+
			"\tStateAssetI AssetState = \"I\"\n"+
			"\tStateAssetJ AssetState = \"J\"\n"+
			"\tStateAssetK AssetState = \"K\"\n"+
			"\tStateAssetL AssetState = \"L\"\n"+
			"\tStateAssetM AssetState = \"M\"\n"+
			"\tStateAssetN AssetState = \"N\"\n"+
			")\n"),
		0o644,
	); err != nil {
		t.Fatalf("write refined canonical file with residue: %v", err)
	}
	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanAssetStateCanonical14(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == assetStateCanonical14Rule {
			t.Errorf("expected zero canonical-14 violations; got rule=%s matched=%s note=%s",
				v.Rule, v.MatchedRule, v.Note)
		}
	}
	if len(r.Warnings) < 1 {
		t.Error("expected at least 1 residue warning (the comment-only Note); got 0")
	}
}

// TestScanAssetStateCanonical14_ThirteenFails verifies the
// sad path: 13 const declarations surface as a single
// canonical-14-count-mismatch violation with note including
// the literal "actual const count: 13".
func TestScanAssetStateCanonical14_ThirteenFails(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeAssetStateValues(t, tempDir, 13, "")
	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanAssetStateCanonical14(tempDir, &policy.Policy{}, r)
	found := 0
	for _, v := range r.Violations {
		if v.Rule == assetStateCanonical14Rule &&
			v.MatchedRule == "canonical_14_count_mismatch" {
			found++
			if !containsSubstring(v.Note, "actual const count: 13") {
				t.Errorf("violation note must surface actual count; got %q", v.Note)
			}
		}
	}
	if found != 1 {
		t.Errorf("expected exactly 1 canonical-14-count-mismatch violation; got %d", found)
	}
}

// TestScanAssetStateCanonical14_FifteenFails verifies the
// upward-drift sad path: 15 const declarations surface as a
// single canonical-14-count-mismatch violation. A future
// agent who silently adds a 15th state (and forgets to
// update gateways) surfaces as a CI build failure.
func TestScanAssetStateCanonical14_FifteenFails(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeAssetStateValues(t, tempDir, 15, "")
	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanAssetStateCanonical14(tempDir, &policy.Policy{}, r)
	found := 0
	for _, v := range r.Violations {
		if v.Rule == assetStateCanonical14Rule &&
			v.MatchedRule == "canonical_14_count_mismatch" {
			found++
			if !containsSubstring(v.Note, "actual const count: 15") {
				t.Errorf("violation note must surface actual count; got %q", v.Note)
			}
		}
	}
	if found != 1 {
		t.Errorf("expected exactly 1 canonical-14-count-mismatch violation; got %d", found)
	}
}

// TestScanAssetStateCanonical14_CanonicalFileMissing verifies
// the godlike/07 fail-closed path: a missing canonical file
// surfaces as a typed violation (not a silent pass).
func TestScanAssetStateCanonical14_CanonicalFileMissing(t *testing.T) {
	tempDir := t.TempDir()
	// Intentionally NOT write the canonical file.
	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanAssetStateCanonical14(tempDir, &policy.Policy{}, r)
	found := 0
	for _, v := range r.Violations {
		if v.Rule == assetStateCanonical14Rule {
			found++
		}
	}
	if found != 1 {
		t.Errorf("expected exactly 1 violation when canonical file is missing; got %d", found)
	}
}

// projectRootFromTestFile resolves the project root from the
// location of the test file (cmd/archcheck/scan/<file>.go).
// Uses runtime.Caller so the path is robust against future
// file moves within the same package. Mirrors the convention
// in the percheck_image_asset_invariants_test sibling.
func projectRootFromTestFile(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed; cannot resolve project root")
	}
	thisDir := filepath.Dir(thisFile)
	// thisDir = <repo>/cmd/archcheck/scan; go up 3 to reach repo root.
	return filepath.Clean(filepath.Join(thisDir, "..", "..", ".."))
}

// TestScanAssetStateCanonical14_ProductionCanonicalFile is the
// END-TO-END SANITY RUN for the canonical-14 gate (PR-CATALOG-
// MULTILINGUA step 7+, July 2026). Opens the REAL production
// canonical file at internal/kernel/asset/asset_state_values.go (NOT
// a synthetic fixture) and asserts the scanner returns ZERO
// violations + at least one residue WARN (the descriptive
// GodLike-07 comment-only StateAsset references inside the
// file).
//
// godlike/06 SSOT discipline: this is the only test in the
// family that exercises the scanner against the production
// canonical surface. If a future agent introduces drift
// (renamed const, removed const, reformatted ValueSpec that
// breaks the regex, etc.), this test exposes the failure at
// `go test ./cmd/archcheck/scan/...` time BEFORE the
// application binary ever runs.
//
// The test does NOT pre-compute "expected count" — it accepts
// whatever the SCANNER itself returns. The lockstep invariant
// lives in three test files in two packages; this one is the
// one that reads the production source.
func TestScanAssetStateCanonical14_ProductionCanonicalFile(t *testing.T) {
	repoRoot := projectRootFromTestFile(t)
	canonical := filepath.Join(repoRoot, assetStateCanonical14Path)
	if _, err := os.Stat(canonical); err != nil {
		t.Fatalf("production canonical file missing at %s: %v (cannot run end-to-end sanity)", assetStateCanonical14Path, err)
	}
	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanAssetStateCanonical14(repoRoot, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == assetStateCanonical14Rule {
			t.Errorf("production canonical %s tripped the canonical-14 gate: rule=%s matched=%s note=%s",
				assetStateCanonical14Path, v.Rule, v.MatchedRule, v.Note)
		}
	}
	// The production canonical file contains comment-only
	// StateAsset references (godlike/06 godoc + per-const
	// doc paragraphs) that the scanner residue-accounts as
	// WARN. At least 1 WARN confirms the residue pathway is
	// alive in CI.
	if len(r.Warnings) < 1 {
		t.Errorf("production canonical %s emitted zero residue warnings; the comment-only residue-accounting pathway may be dead (regression: godlike/07 NO-FAKE-AVAILABILITY drift)",
			assetStateCanonical14Path)
	}
}

// containsSubstring is a stdlib-free helper used by the
// canonical-14 tests; mirrors the helpers in
// percheck_image_asset_invariants.go (sub-project-private
// imports kept minimal per godlike/06 SSOT).
func containsSubstring(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
