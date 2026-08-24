// Package scan — percheck_157_asset_state_migration_default_wire_test.go
// (PR-CATALOG-MULTILINGUA step 7+ GAMMA, July 2026)
//
// Pins the migration 157 DEFAULT literal wire-alignment
// scanner. Builds a synthetic migrations/sqlite/157_*.sql
// file inside t.TempDir() and verifies that the scanner:
//
//   - PASSES  when the column DEFAULT literal equals
//     string(asset.StateAssetDiscovered).
//   - FAILS   when the column DEFAULT literal is renamed
//     to an ad-hoc value (a future agent who renames
//     the typed initial sentinel but forgets to update
//     migration 157 surfaces as a CI build failure).
//   - FAILS   when the canonical migration file is
//     missing (godlike/07 fail-closed).
//   - PASSES  when run against the PRODUCTION canonical
//     migration file (the file at
//     migrations/sqlite/157_asset_state.sql that ships
//     with the repo) — the only test in the family that
//     exercises the scanner against a real file rather
//     than a synthetic fixture.
//
// godlike/07 fail-fast: the scanner does NOT tolerate a
// missing canonical file — each missing-migration test
// asserts the violation count is exactly 1, not 0.
package migrations

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// projectRootForMigration157Test resolves the project root
// from the location of the test file
// (cmd/archcheck/scan/<file>.go). Uses runtime.Caller so
// the path is robust against future file moves within the
// same package. Mirrors projectRootFromTestFile in
// percheck_asset_state_canonical_14_test.go.
func projectRootForMigration157Test(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed; cannot resolve project root")
	}
	thisDir := filepath.Dir(thisFile)
	// thisDir = <repo>/cmd/archcheck/scan; go up 3 to reach repo root.
	return filepath.Clean(filepath.Join(thisDir, "..", "..", ".."))
}

// writeFakeMigration157 writes a synthetic
// migrations/sqlite/157_asset_state.sql inside <tempDir>
// with the given column DEFAULT literal. The rest of the
// file is a minimal scaffolding that mirrors the production
// shape (header godoc + ALTER TABLE + CREATE INDEX).
func writeFakeMigration157(t *testing.T, tempDir, defaultLiteral string) {
	t.Helper()
	dir := filepath.Join(tempDir, "migrations", "sqlite")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fake migration 157 dir: %v", err)
	}
	body := "-- Migration 157 — synthetic test fixture.\n" +
		"-- (real godoc omitted; shape mirrors production.)\n\n" +
		"ALTER TABLE media_assets ADD COLUMN asset_state TEXT NOT NULL DEFAULT '" + defaultLiteral + "';\n\n" +
		"CREATE INDEX IF NOT EXISTS idx_media_assets_asset_state ON media_assets(asset_state);\n"
	path := filepath.Join(dir, "157_asset_state.sql")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fake migration 157: %v", err)
	}
}

// TestScanAssetStateMigration157DefaultWire_CanonicalDiscoveredPasses
// verifies the happy path: a migration with DEFAULT literal
// equal to string(asset.StateAssetDiscovered) trips ZERO
// violations + at least one residue WARN (the godoc header
// comment in the migration file is residue, not violation).
func TestScanAssetStateMigration157DefaultWire_CanonicalDiscoveredPasses(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeMigration157(t, tempDir, string(asset.StateAssetDiscovered))
	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanAssetStateMigration157DefaultWire(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == migration157DefaultRule {
			t.Errorf("expected zero migration-157-default violations; got rule=%s matched=%s note=%s",
				v.Rule, v.MatchedRule, v.Note)
		}
	}
	if len(r.Warnings) < 1 {
		t.Errorf("expected at least 1 residue WARN (the migration header godoc comment is residue); got 0")
	}
}

// TestScanAssetStateMigration157DefaultWire_RenamedSentinelFails
// verifies the canonical-rename drift detection: a future
// agent who adds StateAssetReceived (with value "RECEIVED")
// but forgets to update migration 157 surfaces as a
// SeverityError with a diff message that names BOTH the
// discovered literal AND the expected literal.
func TestScanAssetStateMigration157DefaultWire_RenamedSentinelFails(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeMigration157(t, tempDir, "RECEIVED") // hypothetical renamed initial sentinel.
	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanAssetStateMigration157DefaultWire(tempDir, &policy.Policy{}, r)
	found := 0
	for _, v := range r.Violations {
		if v.Rule == migration157DefaultRule &&
			v.MatchedRule == "migration_157_default_literal_drift" {
			found++
			if !containsSubstring(v.Note, "actual DEFAULT: 'RECEIVED'") {
				t.Errorf("violation note must surface the actual discovered literal; got %q", v.Note)
			}
			wantStr := "want: '" + string(asset.StateAssetDiscovered) + "'"
			if !containsSubstring(v.Note, wantStr) {
				t.Errorf("violation note must surface the expected literal; got %q", v.Note)
			}
		}
	}
	if found != 1 {
		t.Errorf("expected exactly 1 migration-157-default-drift violation; got %d", found)
	}
}

// TestScanAssetStateMigration157DefaultWire_MissingFileFails
// verifies the godlike/07 fail-closed path: a missing
// migration file surfaces as a typed violation (not a
// silent pass).
func TestScanAssetStateMigration157DefaultWire_MissingFileFails(t *testing.T) {
	tempDir := t.TempDir()
	// Intentionally NOT write the migration file.
	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanAssetStateMigration157DefaultWire(tempDir, &policy.Policy{}, r)
	found := 0
	for _, v := range r.Violations {
		if v.Rule == migration157DefaultRule {
			found++
		}
	}
	if found != 1 {
		t.Errorf("expected exactly 1 violation when migration 157 is missing; got %d", found)
	}
}

// TestScanAssetStateMigration157DefaultWire_MissingDefaultClauseFails
// verifies the structural-incompleteness path: the migration
// file is present but has NO `DEFAULT 'literal'` clause.
// The scanner emits a distinct violation (NOT the literal-
// drift variant) because the column default is undefined,
// not drifted.
func TestScanAssetStateMigration157DefaultWire_MissingDefaultClauseFails(t *testing.T) {
	tempDir := t.TempDir()
	dir := filepath.Join(tempDir, "migrations", "sqlite")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Migration without DEFAULT clause (column-not-null but no default).
	if err := os.WriteFile(filepath.Join(dir, "157_asset_state.sql"), []byte(
		"-- Migration 157 — synthetic fixture WITHOUT DEFAULT clause.\n"+
			"ALTER TABLE media_assets ADD COLUMN asset_state TEXT NOT NULL;\n",
	), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanAssetStateMigration157DefaultWire(tempDir, &policy.Policy{}, r)
	found := 0
	for _, v := range r.Violations {
		if v.Rule == migration157DefaultRule &&
			v.MatchedRule == "migration_157_default_literal_drift" &&
			containsSubstring(v.Note, "no `DEFAULT 'literal'` clause") {
			found++
		}
	}
	if found != 1 {
		t.Errorf("expected exactly 1 structural-incompleteness violation; got %d", found)
	}
}

// TestScanAssetStateMigration157DefaultWire_ProductionCanary is
// the END-TO-END SANITY RUN for the migration-157-default
// gate. Opens the REAL production migration 157 file at
// migrations/sqlite/157_asset_state.sql (NOT a synthetic
// fixture) and asserts the scanner returns ZERO violations.
// This is the only test in the family that exercises the
// scanner against the production source.
func TestScanAssetStateMigration157DefaultWire_ProductionCanary(t *testing.T) {
	repoRoot := projectRootForMigration157Test(t)
	canonical := filepath.Join(repoRoot, migration157AssetStatePath)
	if _, err := os.Stat(canonical); err != nil {
		t.Fatalf("production migration 157 file missing at %s: %v (cannot run end-to-end sanity)",
			migration157AssetStatePath, err)
	}
	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanAssetStateMigration157DefaultWire(repoRoot, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == migration157DefaultRule {
			t.Errorf("production migration %s tripped the migration-157-default gate: rule=%s matched=%s note=%s",
				migration157AssetStatePath, v.Rule, v.MatchedRule, v.Note)
		}
	}
}
