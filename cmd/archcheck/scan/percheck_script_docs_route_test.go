// Package scan — hermetic TDD coverage for the Check 63
// forward-prevention gate (percheck_script_docs_route).
//
// percheck_script_docs_route_test.go exercises the
// /api/script-docs/generate route-literal gate via synthetic
// file fixtures in t.TempDir. The tests are decoupled from the
// real repo state so a future refactor of the gate's exclusion
// list or scope filter surfaces as a test failure here BEFORE
// it would land in production.
//
// Coverage matrix:
//
//  1. Canonical package file is NOT flagged (the literal MUST
//     appear in handler.go for the route registration + in
//     module.go for the Descriptor.Name() string — the gate
//     ensures no OTHER internal/api/** package re-references
//     the route).
//
//  2. Test files are NOT flagged (regression guards legitimately
//     reference the literal for invariant pinning; handler
//     _test.go has a TestRegisterRoutes_PinsCanonicalRoute guard
//     that would otherwise trigger a false positive).
//
//  3. Production file in another internal/api/** package
//     containing the literal IS flagged (forward-prevention:
//     catches the "I added a redirect in internal/api/script/
//     handler.go" anti-pattern before it lands in production).
//
//  4. Comment-only file is WARNED, NOT violation (godlike/07
//     no-fake-availability residue accounting — descriptive
//     prose is not a real re-declaration but IS logged so
//     future drift is visible).
//
//  5. Clean file is not flagged and generates no warnings.
//
//  6. Files OUTSIDE the internal/api/ scope (e.g. cmd/server/
//     main.go or internal/application/foo/bar.go) are NEVER
//     visited — the scope filter is the load-bearing
//     forward-prevention (per user spec "bans rg-style
//     references in any internal/api/** package").
//
//  7. Mixed repo layout: realistic scenario with the canonical
//     package + a test file + a clean file + a drift production
//     file + a comment-only file + an out-of-scope drift file.
//     Only the in-scope drift is flagged; the out-of-scope drift
//     is invisible to the gate.
package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeFixtureScriptDocsRoute writes a synthetic .go file at
// the given repo-relative path inside root, creating parent
// directories as needed. Returns the absolute file path. Used
// to build the per-test file layout deterministically.
//
// Mirrors writeFixture from percheck_player_client_test.go;
// kept as a local helper to avoid coupling the two test files
// to a shared test-helpers package (godlike/07 minimum-blast-
// radius — each per-check test file is self-contained).
func writeFixtureScriptDocsRoute(t *testing.T, root, relPath, content string) string {
	t.Helper()
	abs := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir parent of %s: %v", abs, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", abs, err)
	}
	return abs
}

// newEmptyReportScriptDocsRoute returns a freshly-initialised
// Report with the canonical non-nil summary maps. Mirrors
// newEmptyReport from percheck_player_client_test.go.
func newEmptyReportScriptDocsRoute() *report.Report {
	return &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
	}
}

// TestScanScriptDocsRoute_CanonicalPackageFileIsNotFlagged
// is the load-bearing exemption test: the canonical SSOT
// package's files MUST contain the literal without the gate
// flagging them. Mirrors
// TestScanPlayerClientCentralization_CanonicalFileIsNotFlagged.
// If this test fails, the gate has over-zealously excluded
// the canonical owner and the route registration contract is
// broken.
func TestScanScriptDocsRoute_CanonicalPackageFileIsNotFlagged(t *testing.T) {
	root := t.TempDir()
	// Synthetic canonical-surface handler.go: the literal
	// appears in BOTH the package godoc (forward-prevention
	// documentation) AND in the route registration style
	// that mirrors the real handler.go shape (route group
	// prefix + relative path inside the group). Mirrors
	// the production surface at internal/api/script-docs/
	// handler.go:222 + handler.go package godoc.
	canonicalContent := `package scriptdocs

// Package scriptdocs — Handler is the canonical owner of
// POST /api/script-docs/generate. The route is registered
// via the rg.POST call below; do NOT redirect /
// forward / string-reference this route from other
// internal/api/** packages.
type Handler struct{}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	// Group prefix "/api/script-docs" + relative path
	// "/generate" composes the canonical route.
	docs := rg.Group("/api/script-docs")
	docs.POST("/generate", h.Generate)
}
`
	writeFixtureScriptDocsRoute(t, root, "internal/api/script-docs/handler.go", canonicalContent)
	// Synthetic canonical-surface module.go: the literal
	// appears in the Descriptor.Name() string.
	moduleContent := `package scriptdocs

// ScriptDocsDescriptor.Name returns the canonical route
// name: "/api/script-docs/generate".
func (d *ScriptDocsDescriptor) Name() string {
	return "/api/script-docs/generate"
}
`
	writeFixtureScriptDocsRoute(t, root, "internal/api/script-docs/module.go", moduleContent)

	r := newEmptyReportScriptDocsRoute()
	ScanScriptDocsRoute(root, &policy.Policy{}, r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("canonical package files MUST NOT be flagged; got %d violation(s): %+v", got, r.Violations)
	}
	if got := len(r.Warnings); got != 0 {
		t.Fatalf("canonical package files MUST NOT generate warnings; got %d: %+v", got, r.Warnings)
	}
}

// TestScanScriptDocsRoute_TestFilesAreNotFlagged pins the
// regression-guard exemption. Test files legitimately
// reference the literal for invariant pinning
// (handler_test.go has a TestRegisterRoutes_PinsCanonicalRoute
// guard that asserts the literal is the only route
// registration in the package). Excluding tests prevents
// false positives on the regression-guard surface.
//
// Mirrors TestScanPlayerClientCentralization_TestFilesAreNotFlagged.
func TestScanScriptDocsRoute_TestFilesAreNotFlagged(t *testing.T) {
	root := t.TempDir()
	// Synthetic test file referencing the literal in a
	// canonical-route guard — should NOT be flagged.
	testContent := `package scriptdocs

import "testing"

func TestRegisterRoutes_PinsCanonicalRoute(t *testing.T) {
	want := "/api/script-docs/generate"
	if want != "/api/script-docs/generate" {
		t.Fatal("drift detected")
	}
}
`
	writeFixtureScriptDocsRoute(t, root, "internal/api/script-docs/handler_test.go", testContent)
	// And another test file in a DIFFERENT internal/api/**
	// package — also should NOT be flagged (the test file
	// exemption is package-agnostic).
	otherTestContent := `package script

import "testing"

// TestOtherPackage_ReferencesCanonicalRoute is a regression
// guard for the canonical-surface invariant: a test file in
// another internal/api/** package is allowed to reference
// the route literal without triggering a false positive
// (the test-file exemption is package-agnostic). The
// regression it guards is a future change that tightens
// the exemption to package-scoped — which would
// break this guard. The test runs on the same gate as
// the canonical-surface package's own regression guards.
func TestOtherPackage_ReferencesCanonicalRoute(t *testing.T) {
	const route = "/api/script-docs/generate"
	_ = route
}
`
	writeFixtureScriptDocsRoute(t, root, "internal/api/script/script_routes_redirect_test.go", otherTestContent)

	r := newEmptyReportScriptDocsRoute()
	ScanScriptDocsRoute(root, &policy.Policy{}, r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("test files MUST NOT be flagged; got %d violation(s): %+v", got, r.Violations)
	}
}

// TestScanScriptDocsRoute_ProductionFileIsFlagged is the
// load-bearing forward-prevention test: a production .go file
// in another internal/api/** package containing the literal
// MUST be flagged. This catches the "I added a redirect in
// internal/api/script/handler.go" anti-pattern before it lands
// in production. Mirrors
// TestScanPlayerClientCentralization_ProductionFileIsFlagged.
func TestScanScriptDocsRoute_ProductionFileIsFlagged(t *testing.T) {
	root := t.TempDir()
	// Simulate the pre-closure drift: an internal/api/script/
	// handler that builds a string-redirect to the
	// script-docs route (the canonical surface should be
	// the SOLE point of entry — no shadowing/forwarding).
	driftContent := `package script

import (
	"github.com/gin-gonic/gin"
	scriptdocs "github.com/Marcuss-ops/PipelineGen/internal/api/script-docs"
)

// RegisterRoutes is a DRIFT: it shadows the canonical
// /api/script-docs/generate surface with a 200 stub. This is
// the anti-pattern the gate is designed to catch BEFORE
// the regression reaches production.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/api/script-docs/generate", func(c *gin.Context) {
		c.JSON(200, gin.H{"stub": true})
	})
	_ = scriptdocs.ErrReActNotWired
}
`
	relPath := "internal/api/script/script_routes_redirect.go"
	writeFixtureScriptDocsRoute(t, root, relPath, driftContent)

	r := newEmptyReportScriptDocsRoute()
	ScanScriptDocsRoute(root, &policy.Policy{}, r)

	if got := len(r.Violations); got != 1 {
		t.Fatalf("production file with drift MUST be flagged exactly once; got %d violation(s): %+v", got, r.Violations)
	}
	v := r.Violations[0]
	if v.File != relPath {
		t.Errorf("violation file = %q, want %q", v.File, relPath)
	}
	if v.Rule != "percheck_script_docs_route" {
		t.Errorf("violation rule = %q, want percheck_script_docs_route", v.Rule)
	}
	if v.Severity != string(report.SeverityError) {
		t.Errorf("violation severity = %q, want error (forward-prevention gate)", v.Severity)
	}
	if v.MatchedRule != "script_docs_route_canonical_gate" {
		t.Errorf("violation matched_rule = %q, want script_docs_route_canonical_gate", v.MatchedRule)
	}
	if !strings.Contains(v.Note, scriptDocsCanonicalRelPathPrefix) {
		t.Errorf("violation Note must reference the canonical SSOT package %q; got: %s", scriptDocsCanonicalRelPathPrefix, v.Note)
	}
	if !strings.Contains(v.Note, "PR-CHECK-63-SCRIPT-DOCS-ROUTE-2026-07-08") {
		t.Errorf("violation Note must reference PR-CHECK-63-SCRIPT-DOCS-ROUTE-2026-07-08 for historical context; got: %s", v.Note)
	}
	if !strings.Contains(v.Note, "PR-SCRIPT-DOCS-DRIFT-2026-07-08") {
		t.Errorf("violation Note must reference the drift closure PR; got: %s", v.Note)
	}
}

// TestScanScriptDocsRoute_CommentOnlyIsWarned pins the
// godlike/07 no-fake-availability residue-accounting
// behaviour: full-line `//`-prefixed comments that mention
// the literal are NOT surfaced as violations (descriptive
// prose, not a real re-declaration) but ARE logged as
// warnings so future drift is visible in CI output every
// run. Mirrors
// TestScanPlayerClientCentralization_CommentOnlyIsWarned.
func TestScanScriptDocsRoute_CommentOnlyIsWarned(t *testing.T) {
	root := t.TempDir()
	commentOnlyContent := `package someother

// Note: the canonical /api/script-docs/generate route lives
// in internal/api/script-docs/ (per godlike/06 SSOT). See
// PR-SCRIPT-DOCS-DRIFT-2026-07-08 for the closure history.
//
// This file MUST NOT string-reference the route — use
// internal/api/script-docs.Handler.RegisterRoutes instead.
func DoNothing() {}
`
	relPath := "internal/api/someother/comments.go"
	writeFixtureScriptDocsRoute(t, root, relPath, commentOnlyContent)

	r := newEmptyReportScriptDocsRoute()
	ScanScriptDocsRoute(root, &policy.Policy{}, r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("comment-only hits MUST NOT be flagged as violations; got %d: %+v", got, r.Violations)
	}
	// Warn should mention the comment count.
	foundWarn := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "comment-only") && strings.Contains(w, relPath) {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected a warning mentioning the comment-only hit in %s; got warnings: %+v", relPath, r.Warnings)
	}
}

// TestScanScriptDocsRoute_CleanFileIsNotFlagged is the
// negative baseline: a production .go file with no literal
// anywhere must NOT be flagged and must NOT generate
// warnings. Mirrors
// TestScanPlayerClientCentralization_CleanFileIsNotFlagged.
func TestScanScriptDocsRoute_CleanFileIsNotFlagged(t *testing.T) {
	root := t.TempDir()
	cleanContent := `package someother

import "context"

type Service struct{}

func (s *Service) Do(ctx context.Context) error {
	return nil
}
`
	writeFixtureScriptDocsRoute(t, root, "internal/api/someother/clean.go", cleanContent)

	r := newEmptyReportScriptDocsRoute()
	ScanScriptDocsRoute(root, &policy.Policy{}, r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("clean file MUST NOT be flagged; got %d violation(s): %+v", got, r.Violations)
	}
	if got := len(r.Warnings); got != 0 {
		t.Fatalf("clean file MUST NOT generate warnings; got %d: %+v", got, r.Warnings)
	}
}

// TestScanScriptDocsRoute_OutOfScopeFilesAreInvisible pins
// the path-scope filter (godlike/06 SSOT load-bearing
// forward-prevention): files OUTSIDE the internal/api/**
// scope are NEVER visited by the walker, regardless of
// whether they contain the literal. Per user spec, the gate
// is scoped to "any internal/api/** package" — references
// in cmd/, pkg/, internal/application/, etc. are out of
// scope (governed by other policy surfaces).
//
// This is the UNIQUE-to-this-gate test: percheck_player_client
// walks the whole repo, but Check 63 is narrowly scoped.
func TestScanScriptDocsRoute_OutOfScopeFilesAreInvisible(t *testing.T) {
	root := t.TempDir()
	// Out-of-scope drift in cmd/server/main.go: would be a
	// valid forward-prevention concern for ANOTHER gate
	// (e.g. allowed-hosts for HTTP clients) but is OUT OF
	// SCOPE for the script-docs route gate.
	cmdfDriftContent := `package main

// Some CLI hook that string-references the route (e.g.
// for a curl smoke probe). Out of scope for the script-docs
// route gate — this is a different policy concern.
const _route = "/api/script-docs/generate"
`
	writeFixtureScriptDocsRoute(t, root, "cmd/server/main.go", cmdfDriftContent)
	// Out-of-scope drift in internal/application/voiceover/.
	applicationDriftContent := `package voiceover

// Application-layer code that string-references the
// script-docs route. Out of scope.
const _voiceoverRoute = "/api/script-docs/generate"
`
	writeFixtureScriptDocsRoute(t, root, "internal/application/voiceover/cross_ref.go", applicationDriftContent)

	r := newEmptyReportScriptDocsRoute()
	ScanScriptDocsRoute(root, &policy.Policy{}, r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("out-of-scope files MUST NOT trigger violations (path-scope filter is load-bearing); got %d: %+v", got, r.Violations)
	}
	if got := len(r.Warnings); got != 0 {
		t.Fatalf("out-of-scope files MUST NOT generate warnings; got %d: %+v", got, r.Warnings)
	}
}

// TestScanScriptDocsRoute_MixedRepoLayout exercises the
// realistic case: a synthetic repo with the canonical
// package (2 files), a test file, a clean production file, a
// drift production file in another internal/api/** package,
// a comment-only file, and an out-of-scope drift file. Only
// the in-scope drift production file should be flagged; the
// canonical + test + clean files should be silent; the
// comment-only file should generate a warning; the
// out-of-scope drift file should be invisible.
//
// Mirrors TestScanPlayerClientCentralization_MixedRepoLayout
// with one extra dimension (out-of-scope drift) to validate
// the path-scope filter.
func TestScanScriptDocsRoute_MixedRepoLayout(t *testing.T) {
	root := t.TempDir()

	// (1) Canonical SSOT package — 2 files, both silent.
	writeFixtureScriptDocsRoute(t, root, "internal/api/script-docs/handler.go",
		`package scriptdocs
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/api/script-docs/generate", h.Generate)
}
`)
	writeFixtureScriptDocsRoute(t, root, "internal/api/script-docs/module.go",
		`package scriptdocs
func (d *ScriptDocsDescriptor) Name() string { return "/api/script-docs/generate" }
`)

	// (2) Regression-guard test file — silent.
	writeFixtureScriptDocsRoute(t, root, "internal/api/script-docs/handler_test.go",
		`package scriptdocs
import "testing"
func TestX(t *testing.T) { _ = "/api/script-docs/generate" }
`)

	// (3) Clean production file — silent.
	writeFixtureScriptDocsRoute(t, root, "internal/api/clean/clean.go",
		`package clean
func Do() {}
`)

	// (4) Drift production file in another internal/api/**
	// package — THE violation.
	driftRelPath := "internal/api/drift/drift.go"
	writeFixtureScriptDocsRoute(t, root, driftRelPath,
		`package drift
const _ = "/api/script-docs/generate"
`)

	// (5) Comment-only file — warning, not violation.
	commentRelPath := "internal/api/comments/comments.go"
	writeFixtureScriptDocsRoute(t, root, commentRelPath,
		`package comments
// The canonical /api/script-docs/generate route lives in
// internal/api/script-docs/ per godlike/06 SSOT.
`)

	// (6) Out-of-scope drift in cmd/server/ — invisible.
	writeFixtureScriptDocsRoute(t, root, "cmd/server/main.go",
		`package main
const _ = "/api/script-docs/generate"
`)

	r := newEmptyReportScriptDocsRoute()
	ScanScriptDocsRoute(root, &policy.Policy{}, r)

	if got := len(r.Violations); got != 1 {
		t.Fatalf("mixed layout MUST surface exactly 1 violation (the in-scope drift file); got %d: %+v", got, r.Violations)
	}
	if r.Violations[0].File != driftRelPath {
		t.Errorf("violation file = %q, want %q", r.Violations[0].File, driftRelPath)
	}

	// The comment-only file should appear in Warnings.
	foundCommentWarn := false
	for _, w := range r.Warnings {
		if strings.Contains(w, commentRelPath) && strings.Contains(w, "comment-only") {
			foundCommentWarn = true
			break
		}
	}
	if !foundCommentWarn {
		t.Errorf("expected a comment-only warning for %s; got warnings: %+v", commentRelPath, r.Warnings)
	}
}
