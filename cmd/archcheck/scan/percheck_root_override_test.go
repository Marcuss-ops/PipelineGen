// Package scan — hermetic TDD coverage for
// percheck_root_override_ban.
//
// percheck_root_override_test.go exercises the FASE B1
// forward-prevention gate (RootFolderOverride ban in
// application/api layers) via synthetic file fixtures in
// t.TempDir. The tests are decoupled from the real repo state
// so a future refactor of the gate's zone lists surfaces as
// a test failure here BEFORE it would land in production.
//
// Coverage matrix:
//
//  1. Infrastructure file is NOT flagged (the Publisher
//     implementation legitimately uses RootFolderOverride).
//  2. Admin CLI file is NOT flagged (operational overrides
//     in cmd/admin/ are legitimate).
//  3. Application file with RootFolderOverride IS flagged.
//  4. API file with RootFolderOverride IS flagged.
//  5. Comment-only hits are WARN'd, NOT violation.
//  6. Clean production file (no literal) is NOT flagged.
//  7. Test files are NOT flagged.
//  8. Files outside forbidden zones are silently skipped.
//  9. Scanner directory is self-exempt.
//  10. Mixed repo layout: only forbidden-zone non-comment hits
//     are flagged.
package scan

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// TestScanRootOverrideBan_InfrastructureFileNotFlagged ensures
// the Publisher implementation (internal/infrastructure/drive/)
// is NOT flagged — it legitimately constructs PublishRequest
// struct literals with RootFolderOverride.
func TestScanRootOverrideBan_InfrastructureFileNotFlagged(t *testing.T) {
	root := t.TempDir()
	infraContent := `package drive

import "github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"

func buildRequest() delivery.PublishRequest {
	return delivery.PublishRequest{
		Destination:       "youtube_clip",
		Group:             "boxing",
		Subject:           "clip123",
		RootFolderOverride: "some-folder-id",
	}
}
`
	relPath := "internal/infrastructure/drive/publisher.go"
	writeFixture(t, root, relPath, infraContent)

	r := newEmptyReport()
	ScanRootOverrideBan(root, newTestPolicy(), r, false)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("infrastructure file MUST NOT be flagged; got %d violation(s): %+v", got, r.Violations)
	}
}

// TestScanRootOverrideBan_AdminCLIFileNotFlagged ensures
// operator CLIs (cmd/admin/) are NOT flagged.
func TestScanRootOverrideBan_AdminCLIFileNotFlagged(t *testing.T) {
	root := t.TempDir()
	adminContent := `package main

import "github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"

func reconcileFolder() {
	_ = delivery.PublishRequest{
		RootFolderOverride: "override-id",
	}
}
`
	relPath := "cmd/admin/reconcile_qdrant.go"
	writeFixture(t, root, relPath, adminContent)

	r := newEmptyReport()
	ScanRootOverrideBan(root, newTestPolicy(), r, false)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("admin CLI file MUST NOT be flagged; got %d violation(s): %+v", got, r.Violations)
	}
}

// TestScanRootOverrideBan_ApplicationFileFlagged ensures
// application-layer code using RootFolderOverride IS flagged.
func TestScanRootOverrideBan_ApplicationFileFlagged(t *testing.T) {
	root := t.TempDir()
	appContent := `package clips

import "github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"

func uploadClip() {
	_ = delivery.PublishRequest{
		Destination:       "youtube_clip",
		RootFolderOverride: "bypass-path-builder",
	}
}
`
	relPath := "internal/application/clips/bulk_upload_worker.go"
	writeFixture(t, root, relPath, appContent)

	r := newEmptyReport()
	ScanRootOverrideBan(root, newTestPolicy(), r, false)

	if got := len(r.Violations); got != 1 {
		t.Fatalf("application file MUST be flagged exactly once; got %d: %+v", got, r.Violations)
	}
	v := r.Violations[0]
	if v.File != relPath {
		t.Errorf("violation file = %q, want %q", v.File, relPath)
	}
	if v.Rule != "percheck_root_override_ban" {
		t.Errorf("violation rule = %q, want percheck_root_override_ban", v.Rule)
	}
	if v.Severity != string(report.SeverityError) {
		t.Errorf("violation severity = %q, want error", v.Severity)
	}
	if v.MatchedRule != "root_override_forward_prevention_gate" {
		t.Errorf("violation matched_rule = %q, want root_override_forward_prevention_gate", v.MatchedRule)
	}
	if !strings.Contains(v.Note, "RootFolderOverride") {
		t.Errorf("violation Note must reference RootFolderOverride; got: %s", v.Note)
	}
	if !strings.Contains(v.Note, "FASE B1") {
		t.Errorf("violation Note must reference FASE B1; got: %s", v.Note)
	}
}

// TestScanRootOverrideBan_APIFileFlagged ensures API handler
// code using RootFolderOverride IS flagged.
func TestScanRootOverrideBan_APIFileFlagged(t *testing.T) {
	root := t.TempDir()
	apiContent := `package clips

import "github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"

func handleUpload() {
	_ = delivery.PublishRequest{
		Destination:       "youtube_clip",
		RootFolderOverride: "hardcoded-folder",
	}
}
`
	relPath := "internal/api/assets/clips/upload_handler.go"
	writeFixture(t, root, relPath, apiContent)

	r := newEmptyReport()
	ScanRootOverrideBan(root, newTestPolicy(), r, false)

	if got := len(r.Violations); got != 1 {
		t.Fatalf("API file MUST be flagged exactly once; got %d: %+v", got, r.Violations)
	}
	v := r.Violations[0]
	if v.File != relPath {
		t.Errorf("violation file = %q, want %q", v.File, relPath)
	}
	if v.Rule != "percheck_root_override_ban" {
		t.Errorf("violation rule = %q, want percheck_root_override_ban", v.Rule)
	}
}

// TestScanRootOverrideBan_CommentOnlyWarned ensures comment-only
// hits are WARN'd, not violations.
func TestScanRootOverrideBan_CommentOnlyWarned(t *testing.T) {
	root := t.TempDir()
	commentContent := `package clips

// Note: RootFolderOverride is the back-compat escape hatch on
// delivery.PublishRequest. Application code MUST route through
// the typed Publisher surface instead (FASE B1 gate).
func doSomething() {}
`
	relPath := "internal/application/clips/docs.go"
	writeFixture(t, root, relPath, commentContent)

	r := newEmptyReport()
	ScanRootOverrideBan(root, newTestPolicy(), r, false)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("comment-only hits MUST NOT be violations; got %d: %+v", got, r.Violations)
	}
	foundWarn := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "comment-only") && strings.Contains(w, relPath) {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected a comment-only warning for %s; got warnings: %+v", relPath, r.Warnings)
	}
}

// TestScanRootOverrideBan_CleanFileNotFlagged ensures a clean
// production file with no literal is NOT flagged.
func TestScanRootOverrideBan_CleanFileNotFlagged(t *testing.T) {
	root := t.TempDir()
	cleanContent := `package clips

func doWork() error {
	return nil
}
`
	writeFixture(t, root, "internal/application/clips/clean.go", cleanContent)

	r := newEmptyReport()
	ScanRootOverrideBan(root, newTestPolicy(), r, false)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("clean file MUST NOT be flagged; got %d: %+v", got, r.Violations)
	}
	if got := len(r.Warnings); got != 0 {
		t.Fatalf("clean file MUST NOT generate warnings; got %d: %+v", got, r.Warnings)
	}
}

// TestScanRootOverrideBan_TestFilesNotFlagged ensures test
// files are exempt.
func TestScanRootOverrideBan_TestFilesNotFlagged(t *testing.T) {
	root := t.TempDir()
	testContent := `package clips

import "testing"

func TestRootOverride(t *testing.T) {
	// RootFolderOverride is tested here for regression guard.
	_ = "RootFolderOverride"
}
`
	writeFixture(t, root, "internal/application/clips/publisher_test.go", testContent)

	r := newEmptyReport()
	ScanRootOverrideBan(root, newTestPolicy(), r, false)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("test files MUST NOT be flagged; got %d: %+v", got, r.Violations)
	}
}

// TestScanRootOverrideBan_OutsideForbiddenZoneSilentlySkipped
// ensures files outside the forbidden zones (pkg/, internal/domain/,
// cmd/server/, etc.) are NOT scanned.
func TestScanRootOverrideBan_OutsideForbiddenZoneSilentlySkipped(t *testing.T) {
	root := t.TempDir()
	// pkg/ is neither forbidden nor explicitly allowed — it
	// should be silently skipped.
	pkgContent := `package hashutil

// RootFolderOverride is just a string.
const sample = "RootFolderOverride"
`
	writeFixture(t, root, "pkg/hashutil/example.go", pkgContent)

	// internal/domain/ same — silent skip.
	domainContent := `package asset

const note = "RootFolderOverride is in delivery.PublishRequest"
`
	writeFixture(t, root, "internal/kernel/asset/docs.go", domainContent)

	r := newEmptyReport()
	ScanRootOverrideBan(root, newTestPolicy(), r, false)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("outside-forbidden-zone files MUST NOT be flagged; got %d: %+v", got, r.Violations)
	}
}

// TestScanRootOverrideBan_ScannerDirectorySelfExempt ensures
// the archcheck scanner directory is self-exempt.
func TestScanRootOverrideBan_ScannerDirectorySelfExempt(t *testing.T) {
	root := t.TempDir()
	scannerContent := "package scan\n\nconst rootOverrideLiteral = \"RootFolderOverride\"\n"
	relPath := "cmd/archcheck/scan/self_ref.go"
	writeFixture(t, root, relPath, scannerContent)

	r := newEmptyReport()
	ScanRootOverrideBan(root, newTestPolicy(), r, false)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("scanner directory MUST be self-exempt; got %d: %+v", got, r.Violations)
	}
}

// TestScanRootOverrideBan_ProductionOnly_CleanFile_ZeroOutput
// is the wave-flip verifier for the FASE A1-A5 + P12 series:
// after the 5 prior PRs (P12-SOUND-EFFECT-SIDECAR +
// P12-HANDLER-FACADE-SEMANTIC + P12-YOUTUBE-LEGACY-RETIRE +
// P12-CLIPS-AND-BOOKS) reduced the production-code hit
// baseline, a clean production file MUST surface ZERO output
// in production-only mode (no violations, no warnings).
//
// godlike/07 NO-FAKE-AVAILABILITY: the production-only view
// is the operator-facing "zero production-code hits" claim;
// a future drift that re-introduces RootFolderOverride in
// production code surfaces as a violation even in this mode,
// so the baseline check is robust to comment noise.
func TestScanRootOverrideBan_ProductionOnly_CleanFile_ZeroOutput(t *testing.T) {
	root := t.TempDir()
	cleanContent := `package clips

func doWork() error {
	return nil
}
`
	writeFixture(t, root, "internal/application/clips/clean.go", cleanContent)

	r := newEmptyReport()
	ScanRootOverrideBan(root, newTestPolicy(), r, true) // productionOnly=true

	if got := len(r.Violations); got != 0 {
		t.Fatalf("clean file in production-only mode MUST surface 0 violations; got %d: %+v", got, r.Violations)
	}
	if got := len(r.Warnings); got != 0 {
		t.Fatalf("clean file in production-only mode MUST surface 0 warnings; got %d: %+v", got, r.Warnings)
	}
}

// TestScanRootOverrideBan_ProductionOnly_DriftFile_OneViolation
// is the load-bearing forward-prevention pin: a production
// file with the literal in actual code (not in a comment)
// MUST surface exactly one violation in production-only mode.
// The test mirrors the default-mode coverage in
// TestScanRootOverrideBan_ApplicationFileFlagged so the
// production-only path cannot silently weaken the gate.
func TestScanRootOverrideBan_ProductionOnly_DriftFile_OneViolation(t *testing.T) {
	root := t.TempDir()
	driftContent := `package clips

import "github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"

func uploadClip() {
	_ = delivery.PublishRequest{
		Destination:       "youtube_clip",
		RootFolderOverride: "bypass-path-builder",
	}
}
`
	relPath := "internal/application/clips/drift_worker.go"
	writeFixture(t, root, relPath, driftContent)

	r := newEmptyReport()
	ScanRootOverrideBan(root, newTestPolicy(), r, true) // productionOnly=true

	if got := len(r.Violations); got != 1 {
		t.Fatalf("drift file in production-only mode MUST surface exactly 1 violation; got %d: %+v", got, r.Violations)
	}
	v := r.Violations[0]
	if v.File != relPath {
		t.Errorf("violation file = %q, want %q", v.File, relPath)
	}
	if v.Rule != "percheck_root_override_ban" {
		t.Errorf("violation rule = %q, want percheck_root_override_ban", v.Rule)
	}
	if v.Severity != string(report.SeverityError) {
		t.Errorf("violation severity = %q, want error (forward-prevention gate)", v.Severity)
	}
	// Production-only mode silences the comment-only warning
	// bucket; drift files have no comments, so Warnings is
	// empty by construction (this is the operator-facing
	// "zero noise" property of the production-only view).
	if got := len(r.Warnings); got != 0 {
		t.Errorf("drift file in production-only mode MUST surface 0 warnings; got %d: %+v", got, r.Warnings)
	}
}

// TestScanRootOverrideBan_ProductionOnly_CommentOnly_Silenced
// is the wave-flip verifier's noise-reduction contract:
// comment-only hits are SILENCED entirely in production-only
// mode (no warning appended, no violation raised). This
// distinguishes production-only mode from default mode where
// the same file would generate a single comment-only warning
// (per TestScanRootOverrideBan_CommentOnlyWarned).
//
// The semantic intent: production-only = "production-code
// hits" only; comments are documentation, not "hits". A
// future drift that re-introduces the literal in a comment
// will not surface in the production-only view — the operator
// sees only what matters (production-code hits).
func TestScanRootOverrideBan_ProductionOnly_CommentOnly_Silenced(t *testing.T) {
	root := t.TempDir()
	commentContent := `package clips

// Note: RootFolderOverride is the back-compat escape hatch on
// delivery.PublishRequest. Application code MUST route through
// the typed Publisher surface instead (FASE B1 gate).
//
// This file is a doc-only reference. In production-only mode
// the comment-only warning bucket is silenced, so this file
// surfaces 0 violations and 0 warnings.
func doSomething() {}
`
	relPath := "internal/application/clips/docs.go"
	writeFixture(t, root, relPath, commentContent)

	r := newEmptyReport()
	ScanRootOverrideBan(root, newTestPolicy(), r, true) // productionOnly=true

	if got := len(r.Violations); got != 0 {
		t.Fatalf("comment-only hits in production-only mode MUST NOT be violations; got %d: %+v", got, r.Violations)
	}
	// Load-bearing assertion: the comment-only warning bucket
	// is SILENCED in production-only mode (this is the whole
	// point of the flag — noise reduction for the operator
	// baseline check).
	if got := len(r.Warnings); got != 0 {
		t.Fatalf("comment-only hits in production-only mode MUST be SILENCED (no warning); got %d warnings: %+v", got, r.Warnings)
	}
}

// TestScanRootOverrideBan_MixedRepoLayout exercises the
// realistic case with all zone types.
func TestScanRootOverrideBan_MixedRepoLayout(t *testing.T) {
	root := t.TempDir()

	// (1) Infrastructure — allowed
	writeFixture(t, root, "internal/infrastructure/drive/publisher.go",
		`package drive
func build() { _ = delivery.PublishRequest{RootFolderOverride: "x"} }
`)

	// (2) Admin CLI — allowed
	writeFixture(t, root, "cmd/admin/some_tool.go",
		`package main
func run() { _ = delivery.PublishRequest{RootFolderOverride: "x"} }
`)

	// (3) Application — FORBIDDEN
	appRel := "internal/application/clips/worker.go"
	writeFixture(t, root, appRel,
		`package clips
func work() { _ = delivery.PublishRequest{RootFolderOverride: "BAD"} }
`)

	// (4) API — FORBIDDEN
	apiRel := "internal/api/assets/clips/handler.go"
	writeFixture(t, root, apiRel,
		`package clips
func handle() { _ = delivery.PublishRequest{RootFolderOverride: "BAD"} }
`)

	// (5) Clean application file
	writeFixture(t, root, "internal/application/clips/clean.go",
		`package clips
func noop() {}
`)

	// (6) Test file — exempt
	writeFixture(t, root, "internal/application/clips/worker_test.go",
		`package clips
func TestX(t *testing.T) { _ = "RootFolderOverride" }
`)

	// (7) Comment-only in application — warning
	commentRel := "internal/application/clips/docs.go"
	writeFixture(t, root, commentRel,
		`package clips
// RootFolderOverride is not used here.
`)

	// (8) Outside forbidden zone — silent skip
	writeFixture(t, root, "pkg/something/foo.go",
		`package something
const x = "RootFolderOverride"
`)

	r := newEmptyReport()
	ScanRootOverrideBan(root, newTestPolicy(), r, false)

	// Exactly 2 violations: the app drift + the API drift.
	if got := len(r.Violations); got != 2 {
		t.Fatalf("mixed layout MUST surface exactly 2 violations (app + API); got %d: %+v", got, r.Violations)
	}

	vFiles := map[string]bool{}
	for _, v := range r.Violations {
		vFiles[v.File] = true
	}
	if !vFiles[appRel] {
		t.Errorf("expected application drift file %q to be flagged", appRel)
	}
	if !vFiles[apiRel] {
		t.Errorf("expected API drift file %q to be flagged", apiRel)
	}

	// Comment-only warning present.
	foundWarn := false
	for _, w := range r.Warnings {
		if strings.Contains(w, commentRel) && strings.Contains(w, "comment-only") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected a comment-only warning for %s; got warnings: %+v", commentRel, r.Warnings)
	}
}

func TestScanRootOverrideBan_ParentFolderIDApplicationFileFlagged(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/application/clips/drift_parent.go", `package clips

func upload() {
	_ = struct{ ParentFolderID string }{ParentFolderID: "x"}
}
`)

	r := newEmptyReport()
	ScanRootOverrideBan(root, newTestPolicy(), r, true)
	if got := len(r.Violations); got != 1 {
		t.Fatalf("ParentFolderID application usage must be flagged once; got %d: %+v", got, r.Violations)
	}
}

func TestScanRootOverrideBan_ParentFolderIDTransitionalAllowlist(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/capabilities/assets/providers/artlist/destination_service.go", `package artlist

func resolve() {
	_ = struct{ ParentFolderID string }{ParentFolderID: "x"}
}
`)

	r := newEmptyReport()
	ScanRootOverrideBan(root, newTestPolicy(), r, true)
	if got := len(r.Violations); got != 0 {
		t.Fatalf("transitional ParentFolderID caller must be allowlisted; got %d: %+v", got, r.Violations)
	}
}
