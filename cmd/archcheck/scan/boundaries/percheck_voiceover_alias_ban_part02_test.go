package boundaries

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	"path/filepath"
	"strings"
	"testing"
)

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

	writeFixtureAliasBan(t, root, "internal/capabilities/voiceover/service_doc/types.go", `package voiceover_doc

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

	writeFixtureAliasBan(t, root, "internal/capabilities/voiceover/service_doc/types.go", `package voiceover_doc

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
	writeFixtureAliasBan(t, root, "internal/capabilities/voiceover/service_smuggle/types.go", `package voiceover_smuggle

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
			t.Fatalf("expected violation #%d for internal/capabilities/voiceover/service/types.go, got %q", i, v.File)
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
