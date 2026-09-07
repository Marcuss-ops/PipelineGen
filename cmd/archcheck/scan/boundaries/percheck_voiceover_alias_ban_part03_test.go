package boundaries

import (
	"path/filepath"
	"strings"
	"testing"
)

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
	writeFixtureAliasBan(t, root, "internal/capabilities/voiceover/service/job_handler.go", `package voiceover

// JobHandler consumes canonical voiceover types ALL of which
// survived the Sub-PR A/B retirement (the 6 retired aliases are
// absent here on purpose).
type JobHandler struct {
	BatchRequest BatchRequest
	Result       VoiceoverResult
}
`)

	writeFixtureAliasBan(t, root, "internal/capabilities/voiceover/service/persistence/repository.go", `package persistence

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
	writeFixtureAliasBan(t, root, "internal/capabilities/voiceover/service_smuggle/midline.go", `package voiceover_smuggle

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
	writeFixtureAliasBan(t, root, "internal/capabilities/voiceover/service_doc/blockcomment.go", `package voiceover_doc

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
	writeFixtureAliasBan(t, root, "internal/capabilities/voiceover/service_doc/multiline.go", `package voiceover_doc

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
