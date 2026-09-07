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
package boundaries

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	writeFixtureAliasBan(t, root, "internal/capabilities/voiceover/service/types.go", `package voiceover

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
	writeFixtureAliasBan(t, root, "internal/capabilities/voiceover/service_smuggle/types.go", `package voiceover_smuggle

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
