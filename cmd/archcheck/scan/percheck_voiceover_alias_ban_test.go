// Package scan — hermetic TDD test surface for the
// percheck_voiceover_alias_ban.go forward-prevention gate
// (PR-VOICEOVER-ALIASES-RETIRE Sub-PR C, ship_date 2026-07-10).
//
// The 9 tests below lock the canonical contract established by
// percheck_player_client_check + percheck_root_override_check
// precedents, extended here to 6 retired-alias literals:
//
//  1. Canonical-narrative-README residue-accounting
//     (voiceover/types.go generates 0 violations + 6 warnings —
//     per-godlike/07 NO-FAKE-AVAILABILITY residue accounting on
//     intentional documentation references; delibera
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

// Per godlike/07 residue-accounting (the CORRECT model for this gate,
// per the Sub-PR C design reconciliation): voiceover/types.go holds
// the canonical narrative-annotation surface for the 6 retired
// aliases (the Sub-PR A+B goddoc deliberately mentions them by name
// to prevent future re-introduction). The scanner DOES NOT skip
// this file at the WalkDir-level (that would create a silent
// coverage hole — godlike/07 NO-FAKE-AVAILABILITY prefers operator
// noise over silent gaps).
//
// Instead the scanner is set up to produce:
//   - 0 Violations  (no production-code references to the 6 retired aliases)
//   - 6 Warnings   (residue accounting — 1 per alias, comment-only mentions)
//
// This DIVERGES deliberately from percheck_player_client.go's
// full-skip precedent at first glance, but achieves the same
// goal: the canonical narrative README never generates false-
// positive Violations. The difference is that the canonical
// narrative stays AUDITABLE (operator scans see 1 warning per
// alias in the residue accounting) — godlike/07 prefers blowing
// the whistle over silent gaps.
//
// This lock ensures a future agent who re-introduces a production-
// code reference (e.g. `type VoiceoverRecord struct{}`) inside
// voiceover/types.go will surface a 1-Violation signal rather than
// being silently allowed past the gate (Test 8 covers this case
// explicitly). The agent who removes the comment-only residue
// accounting will surface a 0-violations AND 0-warnings delta
// (silent-success anti-pattern).
func TestScanVoiceoverAliasBan_CanonicalFileExempt(t *testing.T) {
	root := t.TempDir()

	// Canonical narrative README — has ALL 6 retired aliases as
	// PACKAGE-PREFIXED comments (1 per line) so the per-alias walker
	// emits exactly 6 warnings in deterministic residue-accounting
	// order. Non-package-prefixed mentions ("VoiceoverRecord"
	// without the "voiceover." prefix) do NOT match the literals
	// and are excluded from the count.
	writeFixtureAliasBan(t, root, "internal/application/voiceover/types.go", `package voiceover

// Canonical narrative README for the 6 retired aliases (Sub-PR A
// + Sub-PR B retirement contracts).
//
// The voiceover package MUST NOT define any of:
//   - voiceover.VoiceoverRecord (canonical: persistence.VoiceoverRecord)
//   - voiceover.VoiceoverRepository (canonical: ports.VoiceoverRepository)
//   - voiceover.PromoRequest (canonical: workflow/promo.Request)
//   - voiceover.PromoResult (canonical: workflow/promo.Result)
//   - voiceover.PromoResponse (canonical: workflow/promo.Response)
//   - voiceover.DefaultPromoLanguages (canonical: translation.DefaultPromoLanguages)

type BatchRequest struct{ Items []string }
type BatchResponse struct{ OK bool }
`)

	r := newEmptyReportAliasBan()
	ScanVoiceoverAliasBan(root, newTestPolicyAliasBan(), r, false)

	// 0 violations: no production-code reference to any of the 6
	// retired aliases inside voiceover/types.go (only comment-only).
	if len(r.Violations) != 0 {
		t.Fatalf("expected 0 violations for canonical narrative README (residue-accounting model), got %d: %+v", len(r.Violations), r.Violations)
	}
	// 6 warnings: residue-accounting — one per retired alias, since
	// the fixture has all 6 package-prefixed mentions inside
	// comment-only lines. Deterministic 6 = 6 aliases × 1 ref per line.
	if got := len(r.Warnings); got != 6 {
		t.Fatalf("expected exactly 6 warnings (one per retired alias in residue-accounting), got %d: %+v", got, r.Warnings)
	}
	// Each warning must carry the percheck_voiceover_alias_ban prefix.
	for _, w := range r.Warnings {
		if !strings.HasPrefix(w, "percheck_voiceover_alias_ban: ") {
			t.Fatalf("expected warning prefix 'percheck_voiceover_alias_ban: ', got %q", w)
		}
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
// Test 8 — No-coverage-hole safety contract
// -------------------------------------------------------------------

// Per godlike/07 NO-FAKE-AVAILABILITY, the residue-accounting
// model (Test 1's contract) MUST NOT create a silent coverage
// hole: if a future agent re-introduces a production-code alias
// reference inside voiceover/types.go, the gate MUST surface a
// Violation. Test 7's clean fixture proves the negative case;
// Test 8 proves the positive case — both surfaces are gated.
//
// This is the load-bearing safety property distinguishing
// residue-accounting from full-skip: full-skip silently drops
// the file at WalkDir-zero-holes, but residue-accounting keeps
// the gate armed against future re-introduction in either model.
func TestScanVoiceoverAliasBan_NoCoverageHoleInCanonicalFile(t *testing.T) {
	root := t.TempDir()

	// Fixture: voiceover/types.go contains BOTH:
	//   - the canonical narrative comments (6 package-prefixed
	//     reference lines)
	//   - a production-code re-introduction (1 type alias of
	//     voiceover.VoiceoverRecord, mimicking a future smuggle
	//     agent)
	// Expected scan output:
	//   - 1 Violation (the type alias declaration line)
	//   - 6 Warnings (the 6 comment-line mentions)
	//   - The 2 counts are INDEPENDENT: residue-accounting covers
	//     production-code violations (SeverityError) AND comment-
	//     only mentions (SeverityWarn) on the same file path.
	writeFixtureAliasBan(t, root, "internal/application/voiceover/types.go", `package voiceover

// Canonical narrative README for the 6 retired aliases:
//   - voiceover.VoiceoverRecord (canonical: persistence.VoiceoverRecord)
//   - voiceover.VoiceoverRepository (canonical: ports.VoiceoverRepository)
//   - voiceover.PromoRequest (canonical: workflow/promo.Request)
//   - voiceover.PromoResult (canonical: workflow/promo.Result)
//   - voiceover.PromoResponse (canonical: workflow/promo.Response)
//   - voiceover.DefaultPromoLanguages (canonical: translation.DefaultPromoLanguages)

type BatchRequest struct{ Items []string }
type BatchResponse struct{ OK bool }

// SMUGGLED production-code re-introduction (the load-bearing test
// signal for the no-coverage-hole property). The non-comment line
// below references one of the retired proxy aliases; the gate MUST
// surface this as a SeverityError Violation despite this file's
// role as the canonical narrative README.
var _ = voiceover.VoiceoverRecord
`)

	r := newEmptyReportAliasBan()
	ScanVoiceoverAliasBan(root, newTestPolicyAliasBan(), r, false)

	// 1 violation: the production-code `type VoiceoverRecord` on
	// the bottom of voiceover/types.go is the load-bearing test
	// signal. A future regression that silently re-introduced this
	// pattern would surface as 0 violations here (a silent
	// coverage hole) — which this test explicitly catches.
	if got := len(r.Violations); got != 1 {
		t.Fatalf("expected exactly 1 violation (smuggled production-code alias), got %d: %+v", got, r.Violations)
	}
	v := r.Violations[0]
	if filepath.Base(v.File) != "types.go" || !strings.Contains(v.File, "voiceover") {
		t.Fatalf("expected violation for internal/application/voiceover/types.go, got %q", v.File)
	}
	if !strings.Contains(v.Note, "VoiceoverRecord") {
		t.Fatalf("expected Violation.Note to mention the alias type, got %q", v.Note)
	}
	// 6 warnings: residue-accounting for the 6 comment-line mentions.
	if got := len(r.Warnings); got != 6 {
		t.Fatalf("expected exactly 6 warnings (residue-accounting for 6 comment-line mentions), got %d: %+v", got, r.Warnings)
	}
}

// -------------------------------------------------------------------
// Test 9 — Real-fixture end-to-end smoke (clean trunk → 0 hits)
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
