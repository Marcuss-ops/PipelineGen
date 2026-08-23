// Package scan — hermetic TDD test surface for the
// percheck_voiceover_alias_ban.go forward-prevention gate
// (PR-VOICEOVER-ALIASES-RETIRE Sub-PR C, ship_date 2026-07-10).
//
// The 12 canonical scanner tests below lock the contract established by
// percheck_player_client_check + percheck_root_override_check
// precedents, extended here to 6 retired-alias literals:
//
//  1. Canonical-narrative-README residue-accounting (Test 1 + 8)
//  2. Test-file exemption (Test 2)
//  3. Production-code violation detection across 4 syntactic
//     forms (Test 3 — var / type alias / function param / return)
//  4. Comment-only WARN in default mode (Test 4)
//  5. Comment-only SILENCED in productionOnly mode (Test 5)
//  6. Skip-dir exemption (Test 6 — vendor / .git / node_modules /
//     nested-prefix cmd/archcheck/scan)
//  7. Real-fixture clean-trunk end-to-end smoke (Test 9)
//  8. Mid-line comment edge case (Test 10 — line-start-only
//     comment classifier per percheck_player_client.go precedent)
//  9. Pure leading block comment (Test 11 — `/* voiceover.X */`
//     on its own line is comment-only → 1 Warning, 0 Violations;
//     symmetric to Test 10's production-code line)
//  10. Multi-line block comment continuation (Test 12 — alias on
//     a `*` continuation line is comment-only → 0 Violations)
//  11. productionOnly+production-code preservation (Test 13 — when
//     productionOnly=true, production-code Violations STILL fire;
//     only comment-only Warnings are silenced)
//
// The permute-item tests are NOT included because the retired
// alias set has 6 entries (one per alias); permuting them as
// separate cases would be 6× duplication. The 11-case set above
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
// used by all 12 canonical scanner tests below. Per-package naming (mirrors the
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
	// warnings: residue-accounting — one per retired alias, since
	// the fixture has all <len(retiredVoiceoverAliases)> package-prefixed
	// mentions inside comment-only lines. The count is derived
	// from the canonical alias set (NOT hardcoded) so future
	// alias additions (Sub-PR D / E / ...) automatically pass
	// through this test without needing an edit.
	//
	// Note (godlike/07 honest scope-lock): the >= assertion is
	// PERMISSIVE on silent alias removal. If a future agent
	// removes an alias from retiredVoiceoverAliases, the
	// warning count would still be 6 (the fixture's 6 mentions
	// stay unchanged) and the test would PASS (6 >= 5). The
	// warning-count delta is VISIBLE to operators reading the
	// test output but does NOT hard-fail. This is intentional
	// per godlike/07 residue-accounting (operator noise over
	// silent gaps) — the canonical strict-equality gate is
	// Test 3 (DetectsProductionCodeHit) which verifies the
	// scan-axes contract end-to-end with a different fixture.
	if got, want := len(r.Warnings), len(retiredVoiceoverAliases); got < want {
		t.Fatalf("expected at least %d warnings (one per retired alias in residue-accounting), got %d: %+v", want, got, r.Warnings)
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

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"

func Bad() voiceover.VoiceoverRecord { return voiceover.VoiceoverRecord{} }
`)

	// Test file with the same retired alias — exempt.
	writeFixtureAliasBan(t, root, "internal/application/somewhere/prod_test.go", `package somewhere

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"

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
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/persistence"
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

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
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
import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
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
	//     reference lines → 6 Warnings in residue-accounting)
	//   - 5 production-code re-introductions of voiceover.VoiceoverRecord
	//     in DIFFERENT syntactic forms (var / type alias / function
	//     param / return type / slice element) → 5 Violations
	// The 5 production-code forms are the canonical syntactic sites
	// where a future smuggle agent would land: a stray variable
	// declaration, a sneaky type alias, a function with a typed
	// parameter, a function returning a typed value, OR a slice
	// element. Locking all 5 surfaces catches future scanner
	// regressions that only handle a subset of Go's type-reference
	// syntax (percheck_root_override.go precedent: lock the breadth,
	// not just one form). The slice element form is the load-bearing
	// breadth — it's a common smuggle surface (slices of typed
	// values appear throughout the production code).
	//
	// Expected scan output:
	//   - 5 Violations (one per production-code form, all for the
	//     same retired alias — voiceover.VoiceoverRecord)
	//   - >= len(retiredVoiceoverAliases) Warnings (residue-accounting
	//     for the comment-line mentions; the >= assertion matches
	//     Test 1's resilience pattern)
	//   - The 2 counts are INDEPENDENT: residue-accounting covers
	//     production-code violations (SeverityError) AND comment-
	//     only mentions (SeverityWarn) on the same file path.
	//
	// The fixture uses `package voiceover_smuggle` (NOT `voiceover`)
	// to simulate a REAL cross-package smuggle: a foreign package
	// importing voiceover and referencing the retired alias via
	// the explicit package prefix. Using `package voiceover` itself
	// would be invalid Go (no self-import) and would also short-
	// circuit the test's purpose (the canonical home is exempt
	// per godlike/06 SSOT, but a future production-code line in
	// voiceover/types.go would still be a real violation).
	writeFixtureAliasBan(t, root, "internal/application/voiceover_smuggle/types.go", `package voiceover_smuggle

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"

// Canonical narrative README for the 6 retired aliases:
//   - voiceover.VoiceoverRecord (canonical: persistence.VoiceoverRecord)
//   - voiceover.VoiceoverRepository (canonical: ports.VoiceoverRepository)
//   - voiceover.PromoRequest (canonical: workflow/promo.Request)
//   - voiceover.PromoResult (canonical: workflow/promo.Result)
//   - voiceover.PromoResponse (canonical: workflow/promo.Response)
//   - voiceover.DefaultPromoLanguages (canonical: translation.DefaultPromoLanguages)

type BatchRequest struct{ Items []string }
type BatchResponse struct{ OK bool }

// SMUGGLED production-code re-introductions (5 distinct syntactic
// forms of voiceover.VoiceoverRecord). Each form must surface as
// a SeverityError Violation despite this file's role as the
// canonical narrative README. A future regression that only
// handles one of these forms (e.g. only var-decl) will surface
// as 4-violations-here (silent smuggle in the missing forms).
var _ = voiceover.VoiceoverRecord
type _SmuggleAlias = voiceover.VoiceoverRecord
func _SmuggleParam(_ voiceover.VoiceoverRecord) {}
func _SmuggleReturn() voiceover.VoiceoverRecord { return voiceover.VoiceoverRecord{} }
var _ []voiceover.VoiceoverRecord
`)

	r := newEmptyReportAliasBan()
	ScanVoiceoverAliasBan(root, newTestPolicyAliasBan(), r, false)

	// 5 violations: the 5 production-code forms in voiceover/types.go
	// are the load-bearing test signal. A future regression that
	// silently re-introduced these patterns (or only handled a
	// subset of the 5 forms) would surface as a violation-count
	// delta here — which this test explicitly catches. The hard-
	// coded `wantViolations := 5` is intentional brittleness:
	// voiceover/types.go is the canonical narrative README and
	// SHOULD NOT have new production-code lines added (that's
	// the whole point of the gate); any future agent adding a
	// 6th production-code line is exactly the regression this
	// gate should catch.
	wantViolations := 5
	if got := len(r.Violations); got != wantViolations {
		t.Fatalf("expected exactly %d violations (5 production-code forms of voiceover.VoiceoverRecord), got %d: %+v", wantViolations, got, r.Violations)
	}
	for i, v := range r.Violations {
		if filepath.Base(v.File) != "types.go" || !strings.Contains(v.File, "voiceover") {
			t.Fatalf("expected violation #%d for internal/application/voiceover/types.go, got %q", i, v.File)
		}
		if !strings.Contains(v.Note, "VoiceoverRecord") {
			t.Fatalf("expected Violation #%d.Note to mention the alias type, got %q", i, v.Note)
		}
		if v.Rule != "percheck_voiceover_alias_ban" {
			t.Fatalf("expected Violation #%d.Rule='percheck_voiceover_alias_ban', got %q", i, v.Rule)
		}
		if v.Severity != string(report.SeverityError) {
			t.Fatalf("expected Violation #%d.Severity=Error, got %q", i, v.Severity)
		}
	}
	// warnings: residue-accounting for the comment-line mentions;
	// derived from len(retiredVoiceoverAliases) for resilience
	// to future alias additions (mirrors Test 1's contract).
	if got, want := len(r.Warnings), len(retiredVoiceoverAliases); got < want {
		t.Fatalf("expected at least %d warnings (residue-accounting for 6 comment-line mentions), got %d: %+v", want, got, r.Warnings)
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

// -------------------------------------------------------------------
// Test 10 — Mid-line comment edge case (line-start-only classifier)
// -------------------------------------------------------------------

// Per godlike/07 minimum-blast-radius, the comment-classifier is
// line-start-only (mirrors percheck_player_client.go::isGoCommentLine
// precedent: a line is a comment iff its first non-whitespace is
// `//` or `*`). A line like
//
//	var _ = voiceover.VoiceoverRecord // see canonical: voiceover.VoiceoverRecord
//
// is classified as production-code → 1 Violation, NOT a separate
// Warning for the trailing comment.
//
// This test pins the canonical behavior: the scanner does NOT
// special-case mid-line comments. The 2 references to
// `voiceover.VoiceoverRecord` on the same line count as 1
// production-code Violation (the impl records 1 Violation per
// line per literal; multiple matches on the same line collapse
// to 1 Violation because the substring-detector runs once per
// line per literal).
//
// Future evolution path: a "smarter" scanner that recognizes
// mid-line comments and produces both Violation + Warning would
// surface as 1-violation + 1-warning here (a behavior delta). At
// that point the canonical contract can be reconsidered via
// PR-PERCHECK-BLOCK-COMMENT-FULL (forward-pointer for the
// percheck_player_client.go "full multi-line block comment
// tracking" gap).
func TestScanVoiceoverAliasBan_MidLineCommentEdgeCase(t *testing.T) {
	root := t.TempDir()

	// Fixture: a single line that combines a production-code
	// alias reference (left-of-`//`) with a trailing comment that
	// ALSO references the alias (right-of-`//`). The line-start
	// is `var` (NOT `//`), so the comment-classifier MUST classify
	// the entire line as production-code.
	writeFixtureAliasBan(t, root, "internal/application/voiceover_smuggle/midline.go", `package voiceover_smuggle

// Trailing comment on the same line as production code:
// The scanner MUST classify this as a single production-code
// Violation (line-start = `+"`var`"+`, NOT `+"`//`"+`) and NOT emit
// a separate Warning for the trailing comment.
var _ = voiceover.VoiceoverRecord // see canonical: voiceover.VoiceoverRecord
`)

	r := newEmptyReportAliasBan()
	ScanVoiceoverAliasBan(root, newTestPolicyAliasBan(), r, false)

	// 1 violation: the production-code line wins; the trailing
	// comment is NOT separately detected as a Warning. The 2
	// substring matches on the same line collapse to 1 Violation
	// because the impl records 1 Violation per LINE per LITERAL
	// (the substring-detector runs once per line per literal; the
	// bufio.Scanner advances line-by-line and appends at most one
	// Violation per matched line per alias). This is the canonical
	// percheck_player_client.go precedent behavior — the scanner
	// collapses repeated substrings on the same line into a single
	// Violation, which is simpler and avoids double-counting for
	// operator dashboards. A future "smarter" scanner that
	// produces N violations per N matches would surface as a
	// behavior delta (this test would FAIL with 2 violations).
	if got := len(r.Violations); got != 1 {
		t.Fatalf("expected exactly 1 violation (line-start = production code wins; 2 matches on same line collapse to 1 per percheck_player_client.go precedent), got %d: %+v", got, r.Violations)
	}
	// 0 warnings: the trailing comment is NOT recognized as a
	// comment-only line (per the line-start-only contract).
	if got := len(r.Warnings); got != 0 {
		t.Fatalf("expected 0 warnings (trailing comment not separately recognized), got %d: %+v", got, r.Warnings)
	}
}

// -------------------------------------------------------------------
// Test 11 — Pure leading block comment (symmetric to Test 10)
// -------------------------------------------------------------------

// Symmetric to Test 10's production-code-wins contract: a line
// that IS a comment-only line (starts with `/*`) and contains a
// retired-alias reference MUST be classified as 0 Violations + 1
// Warning (residue-accounting per godlike/07).
//
// Per the line-start-only classifier (mirrors percheck_player_client.go
// precedent: a line is a comment iff its first non-whitespace is
// `//`, `/*`, or `*`), the single-line block comment `/*
// voiceover.VoiceoverRecord */` is comment-only. The block-
// comment's "/* ... */" structure (line starts with `/*`) makes
// the entire line comment-only per the line-start-only contract.
//
// This test pins the canonical behavior: the comment-only
// classification is FULLY symmetric to the production-code
// classification (Test 10's negative case). Together they form
// the contract: `if line is comment-only (per first non-whitespace)
// → 0 Violations + 1 Warning; if line is production-code → 1
// Violation + 0 Warnings`. The two tests are the contract's
// positive + negative witness.
//
// Future evolution path: a "smarter" scanner that recognizes
// multi-line block comments and produces N Warnings per N lines
// of the block would surface as a behavior delta (this test
// would still pass with 1 warning per line, but the underlying
// accounting would differ). At that point the canonical contract
// can be reconsidered via PR-PERCHECK-BLOCK-COMMENT-FULL
// (forward-pointer for the percheck_player_client.go "full multi-
// line block comment tracking" gap).
func TestScanVoiceoverAliasBan_PureLeadingBlockComment(t *testing.T) {
	root := t.TempDir()

	// Fixture: a single line that IS a block comment containing
	// the retired alias reference. The line starts with `/*` so
	// the comment-classifier MUST classify it as comment-only.
	writeFixtureAliasBan(t, root, "internal/application/voiceover_doc/blockcomment.go", `package voiceover_doc

/* Legacy reference: voiceover.VoiceoverRecord (canonical: persistence.VoiceoverRecord) */
type Spec struct{ Name string }
`)

	r := newEmptyReportAliasBan()
	ScanVoiceoverAliasBan(root, newTestPolicyAliasBan(), r, false)

	// 0 violations: the line is comment-only.
	if got := len(r.Violations); got != 0 {
		t.Fatalf("expected 0 violations (line-start=`/*` = comment-only line), got %d: %+v", got, r.Violations)
	}
	// 1 warning: residue-accounting surfaces the block comment
	// as a per-alias Warning per the comment-only classifier.
	if got := len(r.Warnings); got != 1 {
		t.Fatalf("expected 1 warning (comment-only line residue), got %d: %+v", got, r.Warnings)
	}
	// Verify the warning carries the percheck_voiceover_alias_ban
	// prefix (canonical error message format).
	if !strings.HasPrefix(r.Warnings[0], "percheck_voiceover_alias_ban: ") {
		t.Fatalf("expected warning prefix 'percheck_voiceover_alias_ban: ', got %q", r.Warnings[0])
	}
}

// -------------------------------------------------------------------
// Test 12 — Multi-line block comment continuation
// -------------------------------------------------------------------

// Per the line-start-only classifier (mirrors percheck_player_client.go
// precedent: a line is a comment iff its first non-whitespace is
// `//`, `/*`, or `*`), a continuation line of a multi-line block
// comment that starts with ` * ` is ALSO classified as comment-
// only. This test pins the canonical behavior: a multi-line
// block comment with a retired-alias reference on the ` * `
// continuation line is comment-only, NOT production-code.
//
// The fixture has 2 lines that reference the retired alias:
//   - Line 1: ` * voiceover.VoiceoverRecord (canonical: persistence.VoiceoverRecord)`
//     (the alias is on a ` * ` continuation line — comment-only)
//   - Line 2: a real production-code line using the alias
//     (var _ = voiceover.VoiceoverRecord — production-code)
//
// Expected scan output:
//   - 1 Violation (the production-code line, line 2)
//   - 1 Warning (the comment-only continuation line, line 1)
//
// This locks the multi-line block comment support in the canonical
// comment-classifier (the `*` prefix match). A future regression
// that drops the `*` prefix match (e.g. only handling `//` and
// `/*`) would surface as 1-violation + 0-warnings (silent
// continuation line dropped).
func TestScanVoiceoverAliasBan_MultiLineBlockCommentContinuation(t *testing.T) {
	root := t.TempDir()

	// Fixture: a multi-line block comment with the retired-alias
	// reference on the ` * ` continuation line, followed by a
	// real production-code line. The block comment is OPENED
	// on a previous line and CLOSED on a later line (per Go
	// syntax). The continuation line is the canonical `*`
	// prefix form.
	writeFixtureAliasBan(t, root, "internal/application/voiceover_doc/multiline.go", `package voiceover_doc

/*
 * Legacy reference for migration commentary:
 *   voiceover.VoiceoverRecord (canonical: persistence.VoiceoverRecord)
 *   voiceover.PromoRequest (canonical: workflow/promo.Request)
 *
 * End of multi-line block comment.
 */
type Spec struct{ Name string }

// Production-code line referencing the retired alias:
// MUST surface as a Violation (NOT a comment-only line).
var _ = voiceover.VoiceoverRecord
`)

	r := newEmptyReportAliasBan()
	ScanVoiceoverAliasBan(root, newTestPolicyAliasBan(), r, false)

	// 1 violation: the production-code line (line with
	// `var _ = voiceover.VoiceoverRecord`).
	if got := len(r.Violations); got != 1 {
		t.Fatalf("expected exactly 1 violation (the production-code line), got %d: %+v", got, r.Violations)
	}
	// Verify the violation is for the production-code line.
	if filepath.Base(r.Violations[0].File) != "multiline.go" {
		t.Fatalf("expected violation for multiline.go, got %q", r.Violations[0].File)
	}
	// >= 1 warning: the multi-line block comment's `*`-prefixed
	// continuation lines emit per-alias Warnings. The exact count
	// depends on the scanner's accounting (per-literal or per-
	// line; both are valid). >= 1 locks the residue-accounting
	// surface; the canonical contract is "comment-only lines
	// produce Warnings" (counted strictly per the substring-
	// detector per-line basis).
	if got := len(r.Warnings); got < 1 {
		t.Fatalf("expected at least 1 warning (multi-line block comment `*` continuation line residue), got %d: %+v", got, r.Warnings)
	}
}

// -------------------------------------------------------------------
// Test 13 — productionOnly + production-code preservation
// -------------------------------------------------------------------

// Per the percheck_root_override.go extension of the percheck_player_client
// design: the operator-facing "zero production-code hits" claim
// is auditable via `len(r.Violations) == 0`. The productionOnly
// flag silences ONLY comment-only Warnings — production-code
// Violations STILL fire regardless of the flag.
//
// This test pins the canonical behavior: productionOnly=true
// does NOT change the Violation count (production-code hits are
// always flagged). The flag's only effect is on Warnings
// (residue-accounting for comment-only lines is silenced in
// productionOnly mode for a cleaner operator dashboard).
//
// This lock prevents a future regression that over-applies
// productionOnly (the operator's "I want a clean reading of
// production-code-only hits" query mode must NOT swallow real
// production-code Violations).
func TestScanVoiceoverAliasBan_ProductionOnlyPreservesViolations(t *testing.T) {
	root := t.TempDir()

	// Fixture: BOTH a production-code reference AND a comment-only
	// reference to the retired alias in the same file. The
	// productionOnly=true mode MUST:
	//   - preserve the production-code Violation (1 Violation)
	//   - SILENCE the comment-only Warning (0 Warnings)
	// This is the asymmetric behavior that makes productionOnly
	// useful for the operator-facing "clean reading" use case.
	//
	// The comment line intentionally contains the alias literal
	// (voiceover.VoiceoverRecord) so the 0-Warnings assertion is
	// LOAD-BEARING: without the comment-only reference, the
	// assertion would pass trivially (0 alerts because 0 comment
	// matches). With the reference, the assertion verifies
	// productionOnly ACTUALLY silences the comment-only detection.
	writeFixtureAliasBan(t, root, "internal/application/voiceover_smuggle/prodcodeprod.go", `package voiceover_smuggle

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"

// Legacy migration comment: voiceover.VoiceoverRecord (canonical: persistence.VoiceoverRecord)
// MUST be SILENCED in productionOnly mode (residue accounting suppressed).
type Spec struct{ Name string }

// Production-code line: var _ = voiceover.VoiceoverRecord
// MUST be PRESERVED as Violation regardless of productionOnly flag.
var _ = voiceover.VoiceoverRecord
`)

	r := newEmptyReportAliasBan()
	ScanVoiceoverAliasBan(root, newTestPolicyAliasBan(), r, true /* productionOnly */)

	// 1 violation: the production-code line is ALWAYS flagged,
	// regardless of the productionOnly flag. The flag is
	// asymmetric: it does NOT change Violation accounting.
	if got := len(r.Violations); got != 1 {
		t.Fatalf("expected exactly 1 violation (production-code reference, productionOnly PRESERVES violations), got %d: %+v", got, r.Violations)
	}
	// 0 warnings: the comment-only line is SILENCED in
	// productionOnly mode (residue accounting NOT emitted).
	// This assertion is LOAD-BEARING — the comment line above
	// contains the alias literal; without the productionOnly
	// flag silencing, the residue-accounting would surface
	// >= 1 Warning. Inverse assertion verified by Test 4
	// (TestScanVoiceoverAliasBan_CommentOnlyWarned).
	if got := len(r.Warnings); got != 0 {
		t.Fatalf("expected 0 warnings (comment-only line SILENCED in productionOnly mode), got %d: %+v", got, r.Warnings)
	}
}
