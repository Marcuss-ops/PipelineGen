// Package scan — percheck_qdrant_index_import_ban_test.go
// (bulk YouTube uploader + image ingest drift-fix, July 2026)
//
// Pins the forward-prevention scanner. Builds synthetic .go files
// inside t.TempDir() and verifies that the scanner:
//
//   - PASSES when only the canonical exempt zones
//     (cmd/admin/** + internal/application/jobs/outbox/**)
//     carry the qdrant infrastructure import.
//   - FAILS when a non-canonical internal/application/**
//     package (NOT exempt) imports the qdrant infrastructure.
//   - EXEMPTS _test.go files (regression-guard surface).
//   - EXEMPTS the canonical exempt zones (cmd/admin +
//     outbox worker).
//   - WARNS (does NOT violate) comment-only references to
//     the banned import path.
//   - Is scoped to internal/application/** ONLY: composition
//     root at internal/app/** + CLIs at cmd/server, cmd/worker
//     legitimately import infrastructure packages.
//   - Pins the canonical rule id `percheck_qdrant_index_import_ban`
//     so future renames surface as a loud test failure.
//
// godlike/07 fail-fast: the tests use synthetic .go files
// inside t.TempDir() — no production files are touched at test
// time. The scanner's output for each scenario is asserted
// against an empty Report seeded in each test.
package boundaries

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeFakeAdminCanonical writes a synthetic file in
// cmd/admin/** that legitimately imports the qdrant
// infrastructure (canonical exempt zone — must NOT trip).
func writeFakeAdminCanonical(t *testing.T, tempDir string) string {
	t.Helper()
	dir := filepath.Join(tempDir, "cmd", "admin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir admin dir: %v", err)
	}
	path := filepath.Join(dir, "ad_hoc_admin.go")
	body := "package main\n\n" +
		"// ad_hoc_admin.go: admin tool legitimately reads qdrant\n" +
		"// for data correction. cmd/admin/** is in the exempt set.\n" +
		"import _ \"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write admin file: %v", err)
	}
	return path
}

// writeFakeOutboxCanonical writes a synthetic file in
// internal/application/jobs/outbox/** that legitimately
// imports the qdrant infrastructure (canonical outbox
// worker — must NOT trip).
func writeFakeOutboxCanonical(t *testing.T, tempDir string) string {
	t.Helper()
	dir := filepath.Join(tempDir, "internal", "application", "jobs", "outbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir outbox dir: %v", err)
	}
	path := filepath.Join(dir, "indexing_handle.go")
	body := "package outbox\n\n" +
		"// indexing_handle.go: the canonical IndexingHandler\n" +
		"// consumes asset.index.requested events and routes them\n" +
		"// to the qdrant adapter. Exempt per user directive.\n" +
		"import _ \"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write outbox file: %v", err)
	}
	return path
}

// writeFakeLegacyAuditExempt writes a synthetic file in
// internal/application/qdrant/legacyaudit/** that imports
// the qdrant infrastructure (operator/audit tooling —
// read-only classification walker; must NOT trip).
func writeFakeLegacyAuditExempt(t *testing.T, tempDir string) string {
	t.Helper()
	dir := filepath.Join(tempDir, "internal", "application", "qdrant", "legacyaudit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir legacyaudit dir: %v", err)
	}
	path := filepath.Join(dir, "audit_walker.go")
	body := "package legacyaudit\n\n" +
		"// audit_walker.go: read-only classification walker.\n" +
		"// Reads schema.DefaultV3Schema() for the per-channel\n" +
		"// dimension spec. Operator/audit tooling — exempt\n" +
		"// per the percheck's widened exempt set.\n" +
		"import _ \"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write legacyaudit file: %v", err)
	}
	return path
}

// writeFakeMaintenanceExempt writes a synthetic file in
// internal/application/qdrant/maintenance/** that imports
// the qdrant infrastructure (operator/maintenance tooling
// — 3-mode orchestrator; must NOT trip).
func writeFakeMaintenanceExempt(t *testing.T, tempDir string) string {
	t.Helper()
	dir := filepath.Join(tempDir, "internal", "application", "qdrant", "maintenance")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir maintenance dir: %v", err)
	}
	path := filepath.Join(dir, "service.go")
	body := "package maintenance\n\n" +
		"// service.go: 3-mode orchestrator (audit / repair-locators\n" +
		"// / delete-invalid). Constructs the qdrant client via the\n" +
		"// typed QdrantScannerAdapter + QdrantCleaner ports; Delete\n" +
		"// mode dispatches via the canonical outbox. Operator\n" +
		"// maintenance tooling — exempt per the percheck's widened\n" +
		"// exempt set.\n" +
		"import _ \"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write maintenance file: %v", err)
	}
	return path
}

// writeFakeApplicationViolation writes a synthetic Go file
// inside an internal/application/** package (NOT exempt)
// that imports the qdrant infrastructure directly. The
// fixture filename is `dirty_youtube_uploader.go` so the
// forward-prevention assertion maps to the user's literal
// scenario (bulk YouTube uploader must NOT import qdrant
// directly).
func writeFakeApplicationViolation(t *testing.T, tempDir, fixturePath string) string {
	t.Helper()
	dir := filepath.Join(tempDir, filepath.Dir(fixturePath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	path := filepath.Join(tempDir, fixturePath)
	body := "package clips\n\n" +
		"// dirty_youtube_uploader.go: violation fixture —\n" +
		"// importing the qdrant infrastructure directly from\n" +
		"// internal/application/clips/ is FORBIDDEN. The\n" +
		"// canonical path is CommitAsset → outbox → IndexingHandler.\n" +
		"import _ \"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write violation file: %v", err)
	}
	return path
}

// writeFakeApplicationTestExempt writes a synthetic test file
// inside internal/application/clips/ that imports the qdrant
// infrastructure. _test.go is in the regression-guard
// allowlist — must NOT trip.
func writeFakeApplicationTestExempt(t *testing.T, tempDir string) string {
	t.Helper()
	dir := filepath.Join(tempDir, "internal", "application", "clips")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir test dir: %v", err)
	}
	path := filepath.Join(dir, "bulk_upload_qdrant_test_exempt_test.go")
	body := "package clips\n\n" +
		"// Test fixture: the regression-guard allowlist imports\n" +
		"// the qdrant infrastructure for fixture setup. _test.go\n" +
		"// suffix is exempt from the percheck gate.\n" +
		"import _ \"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write test-exempt file: %v", err)
	}
	return path
}

// writeFakeApplicationCommentOnly writes a synthetic file
// inside internal/application/images/ that ONLY references
// the banned import path in comments. Residue accounting
// (godlike/07) must warn, not violate.
func writeFakeApplicationCommentOnly(t *testing.T, tempDir string) string {
	t.Helper()
	dir := filepath.Join(tempDir, "internal", "application", "images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir comment-only dir: %v", err)
	}
	path := filepath.Join(dir, "doc_residue.go")
	body := "package images\n\n" +
		"// doc_residue.go: ONLY references the banned import\n" +
		"// path in comments. Residue accounting discipline\n" +
		"// (godlike/07) — descriptive prose is non-fatal but\n" +
		"// MUST be flagged as a WARN.\n" +
		"//\n" +
		"// NOTE: the bulk image ingest MUST NOT import\n" +
		"// \"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant\"\n" +
		"// directly. The canonical path is CommitAsset → outbox.\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write comment-only file: %v", err)
	}
	return path
}

// writeFakeCompositionRootIgnores writes a synthetic file at
// internal/app/** (composition root). The composition root
// legitimately wires the qdrant infrastructure (godlike/06 SSOT)
// and is out of scope for this gate.
func writeFakeCompositionRootIgnores(t *testing.T, tempDir string) string {
	t.Helper()
	dir := filepath.Join(tempDir, "internal", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir composition-root dir: %v", err)
	}
	path := filepath.Join(dir, "build_process_qdrant.go")
	body := "package app\n\n" +
		"// build_process_qdrant.go: composition root\n" +
		"// canonical qdrant wiring site. Out of scope for the\n" +
		"// percheck (composition root is the SSE instantiation\n" +
		"// surface, NOT an internal/application/** package).\n" +
		"import _ \"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write composition-root file: %v", err)
	}
	return path
}

// TestScanQdrantIndexImportBan_OnlyExemptPasses verifies the
// happy path: only the canonical exempt surfaces (cmd/admin/**
// + internal/application/jobs/outbox/**) import the qdrant
// infrastructure, so zero violations are emitted.
func TestScanQdrantIndexImportBan_OnlyExemptPasses(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeAdminCanonical(t, tempDir)
	writeFakeOutboxCanonical(t, tempDir)
	writeFakeLegacyAuditExempt(t, tempDir)
	writeFakeMaintenanceExempt(t, tempDir)

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanQdrantIndexImportBan(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == qdrantImportBanRule {
			t.Errorf("expected zero violations when only canonical exempt imports; got rule=%s line=%d note=%s",
				v.Rule, v.Line, v.Note)
		}
	}
}

// TestScanQdrantIndexImportBan_RuleIdStable pins the canonical
// percheck_qdrant_index_import_ban rule id so a future rename
// surfaces as a loud test failure. Matches the family
// precedent (RuleIdStable in percheck_binder_scene_field_writes_test.go).
func TestScanQdrantIndexImportBan_RuleIdStable(t *testing.T) {
	const want = "percheck_qdrant_index_import_ban"
	if qdrantImportBanRule != want {
		t.Errorf("qdrantImportBanRule = %q, want %q (runner.go CheckSpec.Name lockstep)",
			qdrantImportBanRule, want)
	}
}

// TestScanQdrantIndexImportBan_DirtyApplicationFails is the
// load-bearing forward-prevention assertion: a non-canonical
// file inside internal/application/** that imports the qdrant
// infrastructure directly MUST trip the gate with exactly one
// violation.
//
// The fixture filename `dirty_youtube_uploader.go` mirrors the
// user's literal scenario (bulk YouTube uploader regression).
func TestScanQdrantIndexImportBan_DirtyApplicationFails(t *testing.T) {
	tempDir := t.TempDir()
	violatingPath := writeFakeApplicationViolation(t, tempDir,
		"internal/application/clips/dirty_youtube_uploader.go")

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanQdrantIndexImportBan(tempDir, &policy.Policy{}, r)

	found := 0
	for _, v := range r.Violations {
		if v.Rule == qdrantImportBanRule &&
			strings.HasSuffix(v.File, "dirty_youtube_uploader.go") {
			found++
			if v.MatchedRule != "qdrant_index_import_attempt" {
				t.Errorf("MatchedRule = %q, want qdrant_index_import_attempt", v.MatchedRule)
			}
			if !strings.Contains(v.Note, "forbidden") {
				t.Errorf("Note must include 'forbidden'; got %q", v.Note)
			}
			if !strings.Contains(v.Note, "CommitAsset") {
				t.Errorf("Note must include 'CommitAsset' canonical-path reference; got %q", v.Note)
			}
		}
	}
	if found != 1 {
		t.Errorf("expected exactly 1 violation on %s; got %d (all violations: %d)",
			violatingPath, found, len(r.Violations))
	}
}

// TestScanQdrantIndexImportBan_DirtyImageIngestFails is the
// second load-bearing assertion: a non-canonical file inside
// internal/application/images/ that imports the qdrant
// infrastructure directly MUST trip the gate. The fixture
// filename `dirty_image_ingest.go` mirrors the user's literal
// scenario (image ingest regression).
func TestScanQdrantIndexImportBan_DirtyImageIngestFails(t *testing.T) {
	tempDir := t.TempDir()
	violatingPath := writeFakeApplicationViolation(t, tempDir,
		"internal/application/images/dirty_image_ingest.go")

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanQdrantIndexImportBan(tempDir, &policy.Policy{}, r)

	found := 0
	for _, v := range r.Violations {
		if v.Rule == qdrantImportBanRule &&
			strings.HasSuffix(v.File, "dirty_image_ingest.go") {
			found++
		}
	}
	if found != 1 {
		t.Errorf("expected exactly 1 violation on %s; got %d (all violations: %d)",
			violatingPath, found, len(r.Violations))
	}
}

// TestScanQdrantIndexImportBan_TestFileExempted verifies the
// _test.go regression-guard allowlist: a test file inside
// internal/application/clips/ that imports the qdrant
// infrastructure directly MUST NOT trip the gate (test
// fixtures legitimately wire infra adapters).
func TestScanQdrantIndexImportBan_TestFileExempted(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeApplicationTestExempt(t, tempDir)

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanQdrantIndexImportBan(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == qdrantImportBanRule &&
			strings.HasSuffix(v.File, "_test.go") {
			t.Errorf("test file MUST be exempt; got violation: %s", v.Note)
		}
	}
}

// TestScanQdrantIndexImportBan_CommentOnlyIsResidue verifies
// the godlike/07 residue accounting discipline: a comment-
// only reference to the banned import path yields a WARN,
// not a violation.
func TestScanQdrantIndexImportBan_CommentOnlyIsResidue(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeApplicationCommentOnly(t, tempDir)

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanQdrantIndexImportBan(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == qdrantImportBanRule &&
			strings.HasSuffix(v.File, "doc_residue.go") {
			t.Errorf("comment-only references must NOT trip violation; got: %s", v.Note)
		}
	}
	foundWarn := false
	for _, w := range r.Warnings {
		if strings.Contains(w, qdrantImportBanRule) &&
			strings.Contains(w, "doc_residue.go") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected residue warn on doc_residue.go; r.Warnings did not contain it (warnings=%v)", r.Warnings)
	}
}

// TestScanQdrantIndexImportBan_CompositionRootIgnored verifies
// the scope gate: a file at internal/app/** (composition root)
// that imports the qdrant infrastructure MUST NOT trip the
// percheck (composition root is the canonical instantiation
// surface, NOT an internal/application/** package).
func TestScanQdrantIndexImportBan_CompositionRootIgnored(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeCompositionRootIgnores(t, tempDir)

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanQdrantIndexImportBan(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == qdrantImportBanRule &&
			strings.Contains(v.File, "build_process_qdrant.go") {
			t.Errorf("composition-root MUST be out of scope; got violation: %s", v.Note)
		}
	}
}

// TestScanQdrantIndexImportBan_AdminAndOutboxExempted is the
// load-bearing exempt-surface assertion: a file at
// cmd/admin/** OR internal/application/jobs/outbox/** that
// imports the qdrant infrastructure directly MUST NOT trip the
// gate (the canonical exempt set per user directive).
func TestScanQdrantIndexImportBan_AdminAndOutboxExempted(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeAdminCanonical(t, tempDir)
	writeFakeOutboxCanonical(t, tempDir)

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanQdrantIndexImportBan(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == qdrantImportBanRule &&
			(strings.Contains(v.File, "ad_hoc_admin.go") ||
				strings.Contains(v.File, "indexing_handle.go")) {
			t.Errorf("admin/outbox canonical exempt zones MUST be exempt; got violation: %s",
				v.Note)
		}
	}
}

// TestScanQdrantIndexImportBan_LegacyAuditExempted verifies
// that operator/audit tooling under
// internal/application/qdrant/legacyaudit/** is exempt from
// the percheck gate. The fixture file
// `audit_walker.go` imports the qdrant infrastructure for
// read-only classification (schema.DefaultV3Schema) — the
// canonical per-channel dimension spec the walker compares
// against. Exempt per the widened exempt set (Wave YY).
func TestScanQdrantIndexImportBan_LegacyAuditExempted(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeLegacyAuditExempt(t, tempDir)

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanQdrantIndexImportBan(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == qdrantImportBanRule &&
			strings.Contains(v.File, "audit_walker.go") {
			t.Errorf("legacyaudit canonical exempt zone MUST be exempt; got violation: %s",
				v.Note)
		}
	}
}

// TestScanQdrantIndexImportBan_MaintenanceExempted verifies
// that operator/maintenance tooling under
// internal/application/qdrant/maintenance/** is exempt from
// the percheck gate. The fixture file `service.go` imports
// the qdrant infrastructure for the 3-mode orchestrator
// (audit / repair-locators / delete-invalid). Exempt per the
// widened exempt set (Wave YY).
func TestScanQdrantIndexImportBan_MaintenanceExempted(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeMaintenanceExempt(t, tempDir)

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanQdrantIndexImportBan(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == qdrantImportBanRule &&
			strings.Contains(v.File, "maintenance/service.go") {
			t.Errorf("maintenance canonical exempt zone MUST be exempt; got violation: %s",
				v.Note)
		}
	}
}
