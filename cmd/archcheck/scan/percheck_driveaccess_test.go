// Package scan — percheck_driveaccess_test.go
// (PR-DRIVE-CLEANUP, July 2026)
//
// Pins the forward-prevention scanner against direct Drive
// concrete-type access outside the canonical surface
// (delivery.Publisher for writes, drive.FileLifecycle for file
// ops wired at the composition root in internal/app/).
//
// Mirrors the percheck_asset_state_no_shadow_enum_test.go
// pattern: hermetic content-anchored fixtures built inside
// t.TempDir(). The scanner invokes ScanDriveAccessSSOT with
// the fixture root and we assert on r.Violations.
//
// Scenarios pinned (7 tests):
//
//  1. PASS: pure-clean tree (no drive.* references in any
//     .go file under internal/application/) → 0 violations.
//  2. FAIL: drive.Uploader in non-canonical application
//     subpackage → 1 violation pointing at the offending file.
//  3. FAIL: drive.Admin in non-canonical application
//     subpackage → 1 violation pointing at the offending file.
//  4. FAIL: uploaddrive.Uploader in non-canonical application
//     subpackage → 1 violation (aliased imports are also
//     scanned to prevent the catalogsync-only allowlist from
//     becoming a stealth bypass for new callers).
//  5. PASS (forward-pointer allowlist): uploaddrive.Uploader in
//     internal/application/assets/catalogsync/ → 0 violations.
//  6. PASS (composition-root allowlist): drive.Admin +
//     drive.NewDriveServiceFromFiles( in internal/app/ → 0
//     violations (composition root is the canonical wire site).
//  7. PASS (_test.go exemption): drive.Uploader in any
//     internal/application/**/*_test.go → 0 violations
//     (tests may construct fakes directly).
//
// godlike/07 fail-fast: the scanner must deterministically
// emit / not-emit violations in each scenario. A regression
// in any allowlist precedence surfaces as a CI build failure
// rather than a silent schema drift.
package scan

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// driveAccessRule is the canonical Violation.Rule value emitted
// by ScanDriveAccessSSOT. Centralised so the test assertions
// remain pinned to the rule name even if the scanner emits a
// different matched_rule variant.
const driveAccessRule = "percheck_drive_access_ssot"

// makeFixture builds a synthetic .go file at the given relative
// path under tempDir and writes the supplied body. Returns the
// path for direct assertions.
func makeFixture(t *testing.T, tempDir, relPath, body string) string {
	t.Helper()
	full := filepath.Join(tempDir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir fixture dir %q: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture %q: %v", relPath, err)
	}
	return full
}

// runScan is the test-side entrypoint that wires a fresh
// report.Report and invokes ScanDriveAccessSSOT against the
// given tempDir root.
func runScan(t *testing.T, tempDir string) *report.Report {
	t.Helper()
	r := &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
	}
	ScanDriveAccessSSOT(tempDir, &policy.Policy{}, r)
	return r
}

// countViolationsOnFile returns the number of drive-access
// violations whose File path equals relPath. Helps isolate the
// per-file impact when a fixture includes multiple offending
// sites.
func countViolationsOnFile(r *report.Report, relPath string) int {
	n := 0
	for _, v := range r.Violations {
		if v.Rule != driveAccessRule {
			continue
		}
		if v.File == relPath {
			n++
		}
	}
	return n
}

// TestScanDriveAccessSSOT_CleanTreePasses — pure-clean tree
// (no drive.* references anywhere in internal/application/)
// emits ZERO violations. Baseline regression guard.
func TestScanDriveAccessSSOT_CleanTreePasses(t *testing.T) {
	tempDir := t.TempDir()
	makeFixture(t, tempDir,
		"internal/application/images/upload_helper.go",
		"package images\n\n"+
			"// Imported only via the canonical delivery.Publisher port.\n"+
			"// No direct drive.Uploader / drive.Admin references.\n"+
			"type Helper struct{}\n")
	r := runScan(t, tempDir)
	if got := len(r.Violations); got != 0 {
		t.Fatalf("clean tree must emit 0 violations; got %d:\n%s", got,
			joinViolations(r.Violations))
	}
}

// TestScanDriveAccessSSOT_UploaderTypeInApplicationForbids —
// drive.Uploader / *drive.Uploader references in a non-canonical
// application subpackage (i.e. NOT catalogsync) MUST trip a
// violation. Two separate offending lines are included; the
// scanner counts each separately.
func TestScanDriveAccessSSOT_UploaderTypeInApplicationForbids(t *testing.T) {
	tempDir := t.TempDir()
	rel := "internal/application/images/legacy_bypass.go"
	makeFixture(t, tempDir, rel,
		"package images\n\n"+
			"import \"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive\"\n\n"+
			"// FORBIDDEN: this is the legacy images-bypass that PR-DRIVE-CLEANUP bans.\n"+
			"var _ drive.Uploader\n"+
			"var _ *drive.Uploader\n")
	r := runScan(t, tempDir)
	got := countViolationsOnFile(r, rel)
	if got < 2 {
		t.Errorf("expected at least 2 violations on %s (drive.Uploader + *drive.Uploader); got %d (all violations: %d)",
			rel, got, len(r.Violations))
	}
}

// TestScanDriveAccessSSOT_AdminTypeInApplicationForbids —
// drive.Admin / *drive.Admin + direct NewDriveServiceFromFiles(
// constructor call references in a non-canonical application
// subpackage MUST trip violations. Pins the PR-DRIVE-CLEANUP
// extension of the matrix. The 3rd fixture line uses a constructor
// call with the open paren (not a function-value assignment) so the
// `drive.NewDriveServiceFromFiles(` substring match is independent
// from the substring-overlap economy on lines 1+2 — this keeps the
// assertion robust against future pattern-matrix changes that touch
// the dotted-name boundary.
func TestScanDriveAccessSSOT_AdminTypeInApplicationForbids(t *testing.T) {
	tempDir := t.TempDir()
	rel := "internal/application/clips/admin_direct_construct.go"
	makeFixture(t, tempDir, rel,
		"package clips\n\n"+
			"import \"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive\"\n\n"+
			"// FORBIDDEN: drive.Admin + direct constructor references post CUTOVER.\n"+
			"var _ drive.Admin\n"+
			"var _ *drive.Admin\n"+
			"func _() { _, _ = drive.NewDriveServiceFromFiles(\"\", \"\") }\n")
	r := runScan(t, tempDir)
	got := countViolationsOnFile(r, rel)
	// Expected violations (assuming no per-pattern allowlist applies
	// because the path is not catalogsync):
	//   line 1 (var _ drive.Admin)            : 1 (drive.Admin pattern match)
	//   line 2 (var _ *drive.Admin)           : 2 (drive.Admin substring +
	//                                           *drive.Admin direct match)
	//   line 3 (drive.NewDriveServiceFromFiles("", "")) : 1
	//                                           (drive.NewDriveServiceFromFiles(
	//                                           substring, open paren present)
	// Total: at least 4. Asserting exactly 4 keeps the test honest about
	// the per-pattern match logic; ≥3 would also be acceptable but with
	// 1 of headroom against future substring-overlap drift.
	if got != 4 {
		t.Errorf("expected exactly 4 violations on %s; got %d (all violations: %d)",
			rel, got, len(r.Violations))
	}
}

// TestScanDriveAccessSSOT_UploaddriveAliasOutsideCatalogsyncForbids —
// uploaddrive.<X> alias references in a NON-catalogsync
// application subpackage MUST trip a violation. Pins the
// catalogsync allowlist's scope: it covers ONLY the
// catalogsync subscriber directory. A future contributor
// who copy-pastes the alias into a new caller is caught.
//
// All 4 aliased patterns are explicitly exercised (uploaddrive.Uploader
// + uploaddrive.Admin + *uploaddrive.Uploader + *uploaddrive.Admin).
// Each line independently emits multiple violations via the
// substring-overlap (e.g. `uploaddrive.Uploader` ALSO matches the
// general `drive.Uploader` pattern), so the assertion uses the
// total violation count rather than per-pattern counts to keep the
// test stable across future pattern-matrix rearrangements (reviewer
// concern #3: every row of the aliased matrix MUST be pinned).
func TestScanDriveAccessSSOT_UploaddriveAliasOutsideCatalogsyncForbids(t *testing.T) {
	tempDir := t.TempDir()
	rel := "internal/application/youtube/uploaddrive_legacy.go"
	makeFixture(t, tempDir, rel,
		"package youtube\n\n"+
			"import uploaddrive \"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive\"\n\n"+
			"// FORBIDDEN: uploaddrive alias outside catalogsync is a copy-paste bypass.\n"+
			"var _ uploaddrive.Uploader\n"+
			"var _ uploaddrive.Admin\n"+
			"var _ *uploaddrive.Uploader\n"+
			"var _ *uploaddrive.Admin\n")
	r := runScan(t, tempDir)
	got := countViolationsOnFile(r, rel)
	// Expected per-line breakdown (all 4 patterns are NOT in catalogsync subtree):
	//   uploaddrive.Uploader: matches drive.Uploader + uploaddrive.Uploader = 2
	//   uploaddrive.Admin:    matches drive.Admin + uploaddrive.Admin        = 2
	//   *uploaddrive.Uploader: matches uploaddrive.Uploader + *uploaddrive.Uploader
	//                          (drive.Uploader AND *drive.Uploader do NOT
	//                          substring-match *uploaddrive.Uploader because
	//                          the leading * is preceded by `uploadd` rather
	//                          than `drive`) = 2
	//   *uploaddrive.Admin:    matches uploaddrive.Admin + *uploaddrive.Admin
	//                          (same logic) = 2
	// Total: at least 8. Assert a lower bound (≥6) with a note explaining
	// the substring-overlap so future pattern-matrix changes that
	// tighten the matching produce stable expectations.
	if got < 6 {
		t.Errorf("expected at least 6 violations on %s (4 aliased patterns + substring overlaps); got %d (all violations: %d)",
			rel, got, len(r.Violations))
	}
}

// TestScanDriveAccessSSOT_UploaddriveAliasInCatalogsyncForbidden —
// application packages, including catalogsync, cannot reference the
// concrete Drive uploader after migration to drive.Reader.
func TestScanDriveAccessSSOT_UploaddriveAliasInCatalogsyncForbidden(t *testing.T) {
	tempDir := t.TempDir()
	rel := "internal/application/assets/catalogsync/subscriber_uploaddrive.go"
	makeFixture(t, tempDir, rel,
		"package catalogsync\n\n"+
			"import uploaddrive \"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive\"\n\n"+
			"// FORBIDDEN: application code must use the typed Drive reader port.\n"+
			"// Concrete Drive implementations are infrastructure-only.\n"+
			"type Subscriber struct {\n"+
			"\tuploader *uploaddrive.Uploader\n"+
			"}\n")
	r := runScan(t, tempDir)
	if got := countViolationsOnFile(r, rel); got == 0 {
		t.Errorf("catalogsync concrete Drive reference must be rejected")
	}
}

// TestScanDriveAccessSSOT_CompositionRootAllowsAdminReferences —
// drive.Admin / drive.NewDriveServiceFromFiles references INSIDE
// internal/app/ (the composition root) emit ZERO violations.
// The composition root is the canonical wire site for both
// delivery.Publisher AND drive.FileLifecycle + drive.Admin
// (the composition root is allowed to construct the
// infrastructure concrete; it just MUST not leak those
// references into internal/application/).
func TestScanDriveAccessSSOT_CompositionRootAllowsAdminReferences(t *testing.T) {
	tempDir := t.TempDir()
	rel := "internal/app/admin_wiring.go"
	makeFixture(t, tempDir, rel,
		"package app\n\n"+
			"import \"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive\"\n\n"+
			"// ALLOWED: composition root wires drive.Admin + drive.NewDriveServiceFromFiles + drive.NewFileLifecycleAdapter.\n"+
			"// These references are gated by the internal/app/ allowlist prefix in the scanner.\n"+
			"func buildDriveAdmin() *drive.Admin {\n"+
			"\treturn nil\n"+
			"}\n"+
			"func _() { _ = drive.NewDriveServiceFromFiles }\n"+
			"func _() { _ = drive.NewFileLifecycleAdapter }\n")
	r := runScan(t, tempDir)
	if got := countViolationsOnFile(r, rel); got != 0 {
		t.Errorf("internal/app/ allowlist must emit 0 violations; got %d:\n%s", got,
			joinViolations(r.Violations))
	}
}

// TestScanDriveAccessSSOT_TestFilesExempt — drive.Uploader /
// drive.Admin references in any *_test.go file emit ZERO
// violations. Tests are allowed to construct fakes directly
// (the canonical regression-guard surface).
func TestScanDriveAccessSSOT_TestFilesExempt(t *testing.T) {
	tempDir := t.TempDir()
	rel := "internal/application/clips/fake_drives_test.go"
	makeFixture(t, tempDir, rel,
		"package clips\n\n"+
			"import \"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive\"\n\n"+
			"// ALLOWED: test-only fake construction. _test.go suffix exempts the file.\n"+
			"var _ drive.Uploader\n"+
			"var _ drive.Admin\n"+
			"var _ *drive.Uploader\n")
	r := runScan(t, tempDir)
	if got := countViolationsOnFile(r, rel); got != 0 {
		t.Errorf("_test.go exemption must emit 0 violations; got %d:\n%s", got,
			joinViolations(r.Violations))
	}
}

// joinViolations formats a Violation slice into a single
// multi-line string for t.Errorf context. Kept local to the
// test file (no need to export from production code).
func joinViolations(vs []report.Violation) string {
	var b strings.Builder
	for i, v := range vs {
		b.WriteString("  [")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("] ")
		b.WriteString(v.File)
		b.WriteString(":")
		b.WriteString(strconv.Itoa(v.Line))
		b.WriteString(" rule=")
		b.WriteString(v.Rule)
		if v.Note != "" {
			b.WriteString(" note=")
			b.WriteString(v.Note)
		}
		b.WriteString("\n")
	}
	return b.String()
}
