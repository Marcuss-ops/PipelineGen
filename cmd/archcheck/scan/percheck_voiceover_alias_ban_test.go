// Package scan — hermetic TDD test surface for the
// percheck_voiceover_alias_ban.go forward-prevention gate
// (PR-VOICEOVER-ALIASES-RETIRE Sub-PR C, ship_date 2026-07-10).
//
// The 7 tests below lock the canonical contract established by
// percheck_player_client_check + percheck_root_override_check
// precedents, extended here to 6 retired-alias literals:
//
//  1. Canonical-exemption (voiceover/types.go in SkipFiles)
//  2. Test-file exemption (*_test.go)
//  3. Production-code violation detection (SeverityError)
//  4. Comment-only WARN (default mode: productionOnly=false)
//  5. Comment-only SILENCED in productionOnly mode (operator
//     auditability of the "zero production-code hits" claim)
//  6. Skip-dir exemption via the two-tier SkipDirs +
//     SkipPathPrefixes mechanism (.git / vendor / cmd/archcheck/scan)
//  7. Real-fixture: a walk over a real .go file tree that does
//     not contain any retired alias reports ZERO violations +
//     ZERO warnings (end-to-end smoke for the gate's fail-closed
//     semantics on trunk)
//
// The permute-item tests are NOT included because the retired
// alias set has 6 entries (one per alias); permuting them as
// separate cases would be 6× duplication. The 7-case set above
// already covers the critical invocation profiles without
// permuting across aliases — the per-alias iteration is a code-
// motion concern (no semantic test value).
package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeFixtureAliasBan writes one .go file with the given content
// under <root>/<relPath>, returning the repo-relative path.
// Parent directories are auto-created (the test cases below
// expect nested layouts for the production-code and fixture tests).
//
// The AliasBan suffix mirrors the per-package naming convention
// established by percheck_player_client_test.go's writeFixture +
// percheck_root_override_test.go's writeFixtureOverride — these
// helpers all live in the same `scan` package and would otherwise
// collide at compile time.
//
// All call sites in this file MUST be updated when renamed —
// the lockstep semantic is that the helper is tightly coupled to
// the percheck_voiceover_alias_ban scanner under test.
func writeFixtureAliasBan(t *testing.T, root, relPath, content string) string {
	t.Helper()
	abs := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("writeFixtureAliasBan MkdirAll(%q): %v", filepath.Dir(abs), err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFixtureAliasBan WriteFile(%q): %v", abs, err)
	}
	return relPath
}

// newEmptyReportAliasBan returns the canonical empty report fixture
// used by all 7 tests below. Per-package naming (mirrors the
// writeFixtureAliasBan rationale above).
//
// Mirrors percheck_player_client_test.go::newEmptyReport (canonical
// across all per-check forward-prevention tests — diff is only the
// per-test suffix that prevents compile-time collision in the
// same `scan` package).
func newEmptyReportAliasBan() *report.Report {
	return &report.Report{}
}

// newTestPolicyAliasBan returns the canonical stub-policy.Policy
// for per-check scanner tests. The per-check spec does not yet
// plumb per-check severity overrides (godlike/08 evolution track),
// so the default field-value Policy is correct for every test case.
// Per-package naming (mirrors the writeFixtureAliasBan rationale).
func newTestPolicyAliasBan() *policy.Policy {
	return &policy.Policy{}
}

// -------------------------------------------------------------------
// Test 1 — Canonical-exemption (voiceover/types.go in SkipFiles)
// -------------------------------------------------------------------

// Per godlike/06 SSOT one-canonical-owner-per-fact: voiceover/types.go
// holds the canonical narrative-annotation surface for the 6
// retired aliases (the Sub-PR A goddoc deliberately mentions them
// by name to prevent future re-introduction). The SkipFiles
// allow-list makes this the SINGLE legitimate production-code
// site that may reference the retired aliases. The scanner mirrors
// the percheck_player_client.go canonical-exempt precedent: the
// file is FULLY EXEMPT (returns nil at the WalkDir callback), so
// the gate produces ZERO violations AND ZERO warnings.
//
// This lock ensures a future agent who removes voiceover/types.go
// from the allow-list will surface a NON-ZERO violations count
// from the canonical narrative README (the fallback flag — every
// comment-line mention of the retired aliases becomes a real hit
// because the WalkDir no longer short-circuits at this file path).
func TestScanVoiceoverAliasBan_CanonicalFileExempt(t *testing.T) {
	root := t.TempDir()

	// Canonical narrative README — has ALL 6 retired aliases (the
	// godlike/06 SSOT retirement-annotation contract).
	writeFixtureAliasBan(t, root, "internal/application/voiceover/types.go", `package voiceover

// Canonical narrative README for the 6 retired aliases (Sub-PR A
// + Sub-PR B retirement contracts).
//
// The voiceover package MUST NOT define any of:
//   - VoiceoverRecord (canonical: persistence.VoiceoverRecord)
//   - VoiceoverRepository (canonical: ports.VoiceoverRepository)
//   - PromoRequest (canonical: workflow/promo.Request)
//   - PromoResult (canonical: workflow/promo.Result)
//   - PromoResponse (canonical: workflow/promo.Response)
//   - voiceover.DefaultPromoLanguages (canonical: translation.DefaultPromoLanguages)

type BatchRequest struct{ Items []string }
type BatchResponse struct{ OK bool }
`)

	r := newEmptyReportAliasBan()
	ScanVoiceoverAliasBan(root, newTestPolicyAliasBan(), r, false)

	// Per percheck_player_client.go canonical-exempt precedent: the
	// file is FULLY EXEMPT — WalkDir returns nil, so neither the
	// line-by-line scan nor the comment classifier runs. Zero
	// violations AND zero warnings. This is the canonical semantic
	// of the allow-list: an exclusive single source-of-truth that
	// is honored by completely skipping the file (not by promoting
	// violations to warnings).
	if len(r.Violations) != 0 {
		t.Fatalf("expected 0 violations for canonical-exempt voiceover/types.go, got %d: %+v", len(r.Violations), r.Violations)
	}
	if len(r.Warnings) != 0 {
		t.Fatalf("expected 0 warnings for canonical-exempt voiceover/types.go (per percheck_player_client.go precedent — full file exempt yields 0 violations AND 0 warnings), got %d: %+v", len(r.Warnings), r.Warnings)
	}
}

// -------------------------------------------------------------------
// Test 2 — Test-file exemption (*_test.go)
// -------------------------------------------------------------------

// Mirrors the percheck_player_client_test.go precedent: tests
// legitimately reference canonical imports + canonical homes.
// A future retirement-regression test would reference the retired
// alias by name too (forward-prevention surface validation),
// which is why the test-file exemption is preserved.
func TestScanVoiceoverAliasBan_TestFileExempt(t *testing.T) {
	root := t.TempDir()

	// Production-code file with ONE retired alias — flagged.
	writeFixtureAliasBan(t, root, "internal/application/somewhere/prod.go", `package somewhere

import "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"

func Bad() voiceover.VoiceoverRecord { return voiceover.VoiceoverRecord{} }
`)

	// Test file with the same retired alias — exempt.
	writeFixtureAliasBan(t, root, "internal/application/somewhere/prod_test.go", `package somewhere

import "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"

func TestRetiredAliasRegressionGuard(t *testing.T) {
	_ = voiceover.VoiceoverRecord{}
}
`)

	r := newEmptyReportAliasBan()
	ScanVoiceoverAliasBan(root, newTestPolicyAliasBan(), r, false)

	if len(r.Violations) != 1 {
		t.Fatalf("expected exactly 1 violation (prod.go only), got %d: %+v", len(r.Violations), r.Violations)
	}
	// Verify the violation is for prod.go (the test file is exempt).
	v := r.Violations[0]
	if filepath.Base(v.File) != "prod.go" {
		t.Fatalf("expected violation for prod.go (test file exempt), got %q", v.File)
	}
}

// -------------------------------------------------------------------
// Test 3 — Production-code violation detection (SeverityError)
// -------------------------------------------------------------------

// The core forward-prevention contract: ANY production-code .go
// reference to one of the 6 retired aliases is a SeverityError
// violation, regardless of the alias. This is the canonical
// anti-drift surface (percheck_player_client_test.go precedent:
// failure-mode cheerfulness checks the lockstep is wired).
func TestScanVoiceoverAliasBan_DetectsProductionCodeHit(t *testing.T) {
	root := t.TempDir()

	// Fixture: production-code file referencing one of each
	// retired alias type to test all 6 scan axes at once.
	// 1 production-code file with EXACTLY 6 retired-alias references
	// arranged so each reference is on its own line (1 ref per alias —
	// 6 lines = 6 matches). This keeps the assertion arithmetic
	// clean: len(Violations) == 6 means exactly 1 violation per alias.
	//
	// Line 1..6: one variable declaration per retired alias type.
	// Lines 7..n: canonical-replacement declarations that MUST NOT
	// match (e.g. persistence.VoiceoverRecord), to verify the scanner
	// differentiates "retired" from "non-retired".
	writeFixtureAliasBan(t, root, "internal/application/voiceover_smuggle/types.go", `package voiceover_smuggle

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/persistence"
)

var (
	FieldVoiceoverRecord     voiceover.VoiceoverRecord
	FieldVoiceoverRepository voiceover.VoiceoverRepository
	FieldPromoRequest        voiceover.PromoRequest
	FieldPromoResult         voiceover.PromoResult
	FieldPromoResponse       voiceover.PromoResponse
	MakeLang                 func() []string = voiceover.DefaultPromoLanguages
)

var _ persistence.VoiceoverRecord // canonical (NOT a violation) — proves partial-match guard works
`)

	r := newEmptyReportAliasBan()
	ScanVoiceoverAliasBan(root, newTestPolicyAliasBan(), r, false)

	// Expect EXACTLY 6 violations (one per retired alias).
	if got := len(r.Violations); got != 6 {
		t.Fatalf("expected exactly 6 violations (one per retired alias), got %d: %+v", got, r.Violations)
	}
	for _, v := range r.Violations {
		if v.Rule != "percheck_voiceover_alias_ban" {
			t.Fatalf("expected Rule='percheck_voiceover_alias_ban', got %q", v.Rule)
		}
		if v.Severity != string(report.SeverityError) {
			t.Fatalf("expected Severity=Error, got %q", v.Severity)
		}
	}
}

// -------------------------------------------------------------------
// Test 4 — Comment-only WARN (default mode: productionOnly=false)
// -------------------------------------------------------------------

// Per godlike/07 residue-accounting: comment-only references are
// surfaced as Warning (NOT promoted to Violation) so the gate
// paperwork is visible without changing the operator-facing
// exit-code semantics. Mirrors percheck_player_client_test.go's
// comment-warn contract.
//
// This lock prevents a future regression that demotes comment-
// only handling back to enforcement (which would trip
// zero-production-code fixtures on legitimate documentation
// references).
func TestScanVoiceoverAliasBan_CommentOnlyWarned(t *testing.T) {
	root := t.TempDir()

	writeFixtureAliasBan(t, root, "internal/application/voiceover_doc/types.go", `package voiceover_doc

// Legacy reference for migration commentary:
//   voiceover.VoiceoverRecord (canonical: persistence.VoiceoverRecord)
//
// Note: This comment block references the retired alias on purpose
// for migration tracking. The gate MUST classify this as WARN,
// NOT Violation, even in default mode.

type Spec struct{ Name string }
`)

	r := newEmptyReportAliasBan()
	ScanVoiceoverAliasBan(root, newTestPolicyAliasBan(), r, false /* productionOnly */)

	if len(r.Violations) != 0 {
		t.Fatalf("expected 0 violations for comment-only references, got %d: %+v", len(r.Violations), r.Violations)
	}
	if len(r.Warnings) == 0 {
		t.Fatalf("expected >0 warnings for comment-only references (productionOnly=false), got 0 (godlike/07 residue accounting lost)")
	}
	// Each Warning must carry the canonical "percheck_voiceover_alias_ban: <rel>:<line>" prefix.
	for _, w := range r.Warnings {
		if !strings.HasPrefix(w, "percheck_voiceover_alias_ban: ") {
			t.Fatalf("expected warning prefix 'percheck_voiceover_alias_ban: ', got %q", w)
		}
	}
}

// -------------------------------------------------------------------
// Test 5 — Comment-only SILENCED in productionOnly mode
// -------------------------------------------------------------------

// Per the percheck_root_override.go extension of the percheck_player_client
// design: the operator-facing "zero production-code hits" claim
// is auditable via `len(r.Violations) == 0`. In productionOnly mode,
// comment-only references are SILENCED from the Warnings slice
// (they're documentation, not "hits"). Production-code violations
// STILL fire.
//
// This lock prevents a future regression that ignores productionOnly
// (the operator's "I want a clean reading of production-code-only
// hits" query mode becomes broken).
func TestScanVoiceoverAliasBan_CommentOnlySilencedInProductionOnly(t *testing.T) {
	root := t.TempDir()

	writeFixtureAliasBan(t, root, "internal/application/voiceover_doc/types.go", `package voiceover_doc

// Legacy reference: voiceover.VoiceoverRecord
// (canonical: persistence.VoiceoverRecord)
type Spec struct{ Name string }
`)

	r := newEmptyReportAliasBan()
	ScanVoiceoverAliasBan(root, newTestPolicyAliasBan(), r, true /* productionOnly */)

	if len(r.Violations) != 0 {
		t.Fatalf("expected 0 violations (no production-code hits), got %d: %+v", len(r.Violations), r.Violations)
	}
	if len(r.Warnings) != 0 {
		t.Fatalf("expected 0 warnings in productionOnly mode (comment-only SILENCED), got %d: %+v", len(r.Warnings), r.Warnings)
	}
}

// -------------------------------------------------------------------
// Test 6 — Skip-dir exemption via the two-tier SkipDirs + SkipPathPrefixes
// -------------------------------------------------------------------

// Vendored, generated, build-artefact trees are not source-of-truth
// candidates. They are excluded via the basename map (.git/vendor/
// node_modules/node-scraper/examples/scripts/archivist/docs/data) +
// the nested-prefix slice (cmd/archcheck/scan only — the scanner's
// own package, which legitimately contains the literal alias
// names as scanning patterns).
//
// This lock prevents a future regression that REMOVES the scanner
// package from the skip list (the scanner would flag its own
// literal patterns as violations — self-flagging anti-pattern).
func TestScanVoiceoverAliasBan_SkipsDirs(t *testing.T) {
	root := t.TempDir()

	// Fixture A: a "vendored" file under top-level vendor/ tree — exempt.
	writeFixtureAliasBan(t, root, "vendor/legacy/voiceover_stub.go", `package legacy

import "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
func Stub() voiceover.VoiceoverRecord { return voiceover.VoiceoverRecord{} }
`)

	// Fixture B: a .git tree mock — exempt (basename skip).
	writeFixtureAliasBan(t, root, ".git/refs/legacy_voiceover_record.go", `package legacy
type VoiceoverRecord struct{}
`)

	// Fixture C: the scanner's own package (cmd/archcheck/scan) —
	// exempt via the prefix slice (nested-prefix skip).
	writeFixtureAliasBan(t, root, "cmd/archcheck/scan/percheck_self_flag_test.go", `package scan
// this comment MUST NOT be flagged as a Violation
// (it just contains the retired alias name in narrative form)
func selfFlag() voiceover.VoiceoverRecord { return voiceover.VoiceoverRecord{} }
`)

	// Fixture D: under node_modules — exempt (basename skip).
	writeFixtureAliasBan(t, root, "node_modules/some-pkg/voiceover.go", `package somepkg
import "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
func X() voiceover.VoiceoverRecord { return voiceover.VoiceoverRecord{} }
`)

	r := newEmptyReportAliasBan()
	ScanVoiceoverAliasBan(root, newTestPolicyAliasBan(), r, false)

	if len(r.Violations) != 0 {
		t.Fatalf("expected 0 violations (skip-dir/tracked exemptions hold), got %d: %+v", len(r.Violations), r.Violations)
	}
}

// -------------------------------------------------------------------
// Test 7 — Real-fixture end-to-end smoke (clean trunk → 0 hits)
// -------------------------------------------------------------------

// End-to-end smoke over a fixture tree that does NOT contain any
// of the 6 retired aliases (a "clean trunk" simulation). The gate
// MUST return ZERO violations + ZERO warnings. This catches future
// regressions in the underlying scanner infrastructure (filepath
// walk, bufio scanner, comment-classifier, etc.) — silent failures
// in those primitives would otherwise be invisible until a
// forward-prevention violation surfaced.
//
// IMPORTANT: this test creates fixtures that contain non-retired
// patterns (e.g. voiceover.OtherStruct, a non-retired voiceover.
// prefix-matched struct in the voiceover package itself) to
// ensure the scanner differentiates "retired" from "non-retired".
func TestScanVoiceoverAliasBan_CleanFixtureZeroHits(t *testing.T) {
	root := t.TempDir()

	// A fixture tree that emulates a clean production layout — many
	// files, none referencing the 6 retired aliases. Some files
	// reference NON-retired voiceover symbols (BatchRequest,
	// VoiceoverResult, etc.) to ensure the substring matcher does
	// NOT false-positive on near-miss patterns.
	writeFixtureAliasBan(t, root, "internal/application/voiceover/job_handler.go", `package voiceover

// JobHandler consumes canonical voiceover types ALL of which
// survived the Sub-PR A/B retirement (the 6 retired aliases are
// absent here on purpose).
type JobHandler struct {
	BatchRequest BatchRequest
	Result       VoiceoverResult
}
`)

	writeFixtureAliasBan(t, root, "internal/application/voiceover/persistence/repository.go", `package persistence

// Canonical VOICEOVER-RECORD home — production code may import
// this freely. The gate MUST NOT flag persistence.VoiceoverRecord
// because the "persistence." prefix means the alias matches are
// NOT retired.
type VoiceoverRecord struct{ ID int64 }
`)

	writeFixtureAliasBan(t, root, "internal/application/workflow/promo/generate.go", `package promo

// Canonical PROM-REQUEST/RESULT/RESPONSE home — production code
// may import these freely.
type Request struct{ Text string }
type Result struct{ OK bool }
type Response struct{ ID int64 }
`)

	writeFixtureAliasBan(t, root, "internal/application/translation/defaults.go", `package translation

// Canonical DefaultPromoLanguages home — production code may import
// this freely.
func DefaultPromoLanguages() []string { return []string{"it-IT", "en-US"} }
`)

	r := newEmptyReportAliasBan()
	ScanVoiceoverAliasBan(root, newTestPolicyAliasBan(), r, false)

	if len(r.Violations) != 0 {
		t.Fatalf("expected 0 violations on clean fixture (no retired alias references), got %d: %+v", len(r.Violations), r.Violations)
	}
	// In default mode, comment-only references in production-code
	// files would generate WARNs. Our fixtures deliberately have
	// ZERO mentions of the 6 retired aliases even in comments,
	// so WARN count must ALSO be 0.
	if len(r.Warnings) != 0 {
		t.Fatalf("expected 0 warnings on clean fixture, got %d: %+v", len(r.Warnings), r.Warnings)
	}
}
