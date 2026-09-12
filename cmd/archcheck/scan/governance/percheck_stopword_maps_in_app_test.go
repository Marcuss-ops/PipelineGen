// Package scan — test for ScanStopwordMapsInApp
// (percheck_stopword_maps_in_app forward-prevention gate).
//
// Hermetic (t.TempDir-anchored). Validates the core invariants of the
// hardcoded-stop-word-map gate:
//
//  1. A production file under internal/ containing a
//     single-line stop-word map (`map[string]struct{}{"the": {}}`)
//     trips the gate as SeverityError.
//  2. The codebase norm — an expanded multi-line literal (opener on its
//     own line, one quoted word per body line) — trips the gate at the
//     opener line (this was the historical blind spot that left the gate
//     dead even when invoked).
//  3. Nested per-language marker maps
//     (`map[string]map[string]struct{}{...}`) trip the gate.
//  4. A `map[string]struct{}{}` with NO stop-word-like keys does NOT
//     trip (legitimate non-linguistic maps stay legal).
//  5. Files outside internal/ are out of scope (pkg/ is never scanned),
//     while the canonical lexicon home (internal/capabilities/linguistics/)
//     is exempt through the rule's Owners set.
//  6. Test files are exempt (regression-guard surface).
//  7. A file carrying a `LEXICON_MIRROR_DEBT: owner=…, deadline=…` marker
//     defers its maps to a residue WARNING while the deadline is ahead and
//     fails closed once it passes (a marker without a deadline exempts
//     nothing).
package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// makeStopwordFixture writes a fixture .go file at the requested
// repo-relative path inside `root`. Mirrors the family helper idiom.
func makeStopwordFixture(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestScanStopwordMapsInApp_SingleLineMap_TripsGate verifies the
// canonical violation shape for a single-line map literal.
func TestScanStopwordMapsInApp_SingleLineMap_TripsGate(t *testing.T) {
	root := t.TempDir()
	makeStopwordFixture(t, root, "internal/capabilities/scripts/usecase/hardcoded.go",
		`package usecase

var hardcoded = map[string]struct{}{"the": {}, "and": {}, "for": {}}
`)
	rep := &report.Report{}
	ScanStopwordMapsInApp(root, nil, rep)
	if len(rep.Violations) == 0 {
		t.Fatalf("expected ≥ 1 violation; got 0 (single-line stop-word map must trip gate)")
	}
	if rep.Violations[0].Rule != stopwordMapRule {
		t.Fatalf("violation rule = %q, want %q", rep.Violations[0].Rule, stopwordMapRule)
	}
	if rep.Violations[0].Severity != string(report.SeverityError) {
		t.Fatalf("violation severity = %q, want SeverityError", rep.Violations[0].Severity)
	}
}

// TestScanStopwordMapsInApp_MultiLineMap_TripsGate verifies the codebase
// norm: the opener on its own line followed by one word per body line.
// This is the exact shape of the legacy hardcoded maps (researchStopWords
// etc.) that the gate exists to ban — a shape the pre-registration
// same-line-only regex could never catch.
func TestScanStopwordMapsInApp_MultiLineMap_TripsGate(t *testing.T) {
	root := t.TempDir()
	makeStopwordFixture(t, root, "internal/capabilities/scripts/usecase/hardcoded.go",
		`package usecase

var researchStopWords = map[string]struct{}{
	"the": {}, "and": {}, "of": {}, "to": {}, "in": {},
	"il": {}, "lo": {}, "la": {},
}
`)
	rep := &report.Report{}
	ScanStopwordMapsInApp(root, nil, rep)
	if len(rep.Violations) != 1 {
		t.Fatalf("expected 1 violation anchored at the opener; got %d", len(rep.Violations))
	}
	// The violation is anchored at the map opener line, not the word line.
	if got := rep.Violations[0].Line; got != 3 {
		t.Fatalf("violation line = %d, want 3 (the map opener)", got)
	}
}

// TestScanStopwordMapsInApp_NestedLanguageMarkers_TripsGate verifies the
// nested per-language marker shape
// (`map[string]map[string]struct{}{...}`) — the researchLanguageMarkers
// form — trips the gate.
func TestScanStopwordMapsInApp_NestedLanguageMarkers_TripsGate(t *testing.T) {
	root := t.TempDir()
	makeStopwordFixture(t, root, "internal/capabilities/scripts/usecase/hardcoded.go",
		`package usecase

var markers = map[string]map[string]struct{}{
	"en": {"the": {}, "and": {}, "of": {}},
	"it": {"il": {}, "lo": {}, "la": {}},
}
`)
	rep := &report.Report{}
	ScanStopwordMapsInApp(root, nil, rep)
	if len(rep.Violations) == 0 {
		t.Fatalf("expected ≥ 1 violation; got 0 (nested language-marker map must trip gate)")
	}
}

// TestScanStopwordMapsInApp_NonStopwordMap_NoTrip verifies a
// map[string]struct{}{} whose keys are NOT stop-word-like stays legal.
func TestScanStopwordMapsInApp_NonStopwordMap_NoTrip(t *testing.T) {
	root := t.TempDir()
	makeStopwordFixture(t, root, "internal/capabilities/config/set.go",
		`package config

var allowedKinds = map[string]struct{}{
	"video": {}, "image": {}, "audio": {}, "music": {},
}
`)
	rep := &report.Report{}
	ScanStopwordMapsInApp(root, nil, rep)
	if len(rep.Violations) != 0 {
		t.Fatalf("legitimate map tripped gate: got %d violations\nfirst: %s",
			len(rep.Violations), rep.Violations[0].Note)
	}
}

// TestScanStopwordMapsInApp_CanonicalLexiconExempt verifies the canonical
// lexicon home (internal/capabilities/linguistics/) is never scanned — the
// gate's Owners set declares it the legitimate owner of linguistic data.
func TestScanStopwordMapsInApp_CanonicalLexiconExempt(t *testing.T) {
	root := t.TempDir()
	makeStopwordFixture(t, root, "internal/capabilities/linguistics/lexicon_builtin.go",
		`package linguistics

var stopWords = map[string]struct{}{
	"the": {}, "and": {}, "of": {},
}
`)
	rep := &report.Report{}
	ScanStopwordMapsInApp(root, nil, rep)
	if len(rep.Violations) != 0 {
		t.Fatalf("canonical lexicon tripped gate: got %d violations", len(rep.Violations))
	}
}

// TestScanStopwordMapsInApp_OutOfScopePkg_NoTrip verifies pkg/ is out of
// scope (only internal/application/ and internal/infrastructure/ are
// scanned).
func TestScanStopwordMapsInApp_OutOfScopePkg_NoTrip(t *testing.T) {
	root := t.TempDir()
	makeStopwordFixture(t, root, "pkg/textutil/textutil.go",
		`package textutil

var stop = map[string]struct{}{"the": {}, "and": {}}
`)
	rep := &report.Report{}
	ScanStopwordMapsInApp(root, nil, rep)
	if len(rep.Violations) != 0 {
		t.Fatalf("pkg/ file tripped gate: got %d violations", len(rep.Violations))
	}
}

// TestScanStopwordMapsInApp_DebtMarkerFutureDeadline_WarnsNotFails verifies
// the LEXICON_MIRROR_DEBT deferral: a marked file keeps the debt VISIBLE as a
// residue warning (so it appears in every report) without failing the gate
// while the deadline is in the future.
func TestScanStopwordMapsInApp_DebtMarkerFutureDeadline_WarnsNotFails(t *testing.T) {
	root := t.TempDir()
	makeStopwordFixture(t, root, "internal/capabilities/scripts/usecase/hardcoded.go",
		`package usecase

// LEXICON_MIRROR_DEBT: owner=@scripts, deadline=2999-01-01 — migrate to the LexiconRegistry.
var hardcoded = map[string]struct{}{
	"the": {}, "and": {},
}
`)
	rep := &report.Report{}
	ScanStopwordMapsInApp(root, nil, rep)
	if len(rep.Violations) != 0 {
		t.Fatalf("deferred map must not fail while the deadline is in the future; got %d violations: %+v",
			len(rep.Violations), rep.Violations)
	}
	if len(rep.Warnings) != 1 {
		t.Fatalf("expected exactly 1 residue warning for the deferred map; got %d (%v)",
			len(rep.Warnings), rep.Warnings)
	}
	if !strings.Contains(rep.Warnings[0], "internal/capabilities/scripts/usecase/hardcoded.go") {
		t.Fatalf("residue warning must name the deferred file; got %q", rep.Warnings[0])
	}
}

// TestScanStopwordMapsInApp_DebtMarkerExpired_FailsClosed verifies the
// exemption re-arms: once the declared deadline has passed the same maps are
// violations again, so a marker can never authorise permanent debt.
func TestScanStopwordMapsInApp_DebtMarkerExpired_FailsClosed(t *testing.T) {
	root := t.TempDir()
	makeStopwordFixture(t, root, "internal/capabilities/scripts/usecase/hardcoded.go",
		`package usecase

// LEXICON_MIRROR_DEBT: owner=@scripts, deadline=2000-01-01 — overdue.
var hardcoded = map[string]struct{}{
	"the": {}, "and": {},
}
`)
	rep := &report.Report{}
	ScanStopwordMapsInApp(root, nil, rep)
	if len(rep.Violations) != 1 {
		t.Fatalf("expired deferral must fail closed with 1 violation; got %d (%+v)",
			len(rep.Violations), rep.Violations)
	}
	if !strings.Contains(rep.Violations[0].Note, "expired") {
		t.Fatalf("expired-deadline violation must say so; got %q", rep.Violations[0].Note)
	}
}

// TestScanStopwordMapsInApp_DebtMarkerWithoutDeadline_FailsClosed verifies a
// marker that carries no parseable deadline exempts nothing (an exemption
// without an expiry is permanent debt).
func TestScanStopwordMapsInApp_DebtMarkerWithoutDeadline_FailsClosed(t *testing.T) {
	root := t.TempDir()
	makeStopwordFixture(t, root, "internal/capabilities/scripts/usecase/hardcoded.go",
		`package usecase

// LEXICON_MIRROR_DEBT: owner=@scripts.
var hardcoded = map[string]struct{}{
	"the": {}, "and": {},
}
`)
	rep := &report.Report{}
	ScanStopwordMapsInApp(root, nil, rep)
	if len(rep.Violations) != 1 {
		t.Fatalf("deadline-less marker must fail closed with 1 violation; got %d (%+v)",
			len(rep.Violations), rep.Violations)
	}
	if !strings.Contains(rep.Violations[0].Note, "no valid") {
		t.Fatalf("deadline-less violation must explain the missing deadline; got %q", rep.Violations[0].Note)
	}
}

// TestScanStopwordMapsInApp_DebtMarkerWithoutMap_NoResidue verifies a marker
// on a file with no hardcoded map produces neither a violation nor a warning
// (the deferral only accounts for real residue).
func TestScanStopwordMapsInApp_DebtMarkerWithoutMap_NoResidue(t *testing.T) {
	root := t.TempDir()
	makeStopwordFixture(t, root, "internal/capabilities/scripts/usecase/clean.go",
		`package usecase

// LEXICON_MIRROR_DEBT: owner=@scripts, deadline=2000-01-01 — no maps left.
var allowedKinds = map[string]struct{}{"video": {}, "image": {}}
`)
	rep := &report.Report{}
	ScanStopwordMapsInApp(root, nil, rep)
	if len(rep.Violations) != 0 || len(rep.Warnings) != 0 {
		t.Fatalf("cleaned file must be silent; got violations=%d warnings=%d",
			len(rep.Violations), len(rep.Warnings))
	}
}

// TestScanStopwordMapsInApp_TestFileExempt verifies _test.go files are
// exempt (regression-guard surface).
func TestScanStopwordMapsInApp_TestFileExempt(t *testing.T) {
	root := t.TempDir()
	makeStopwordFixture(t, root, "internal/capabilities/scripts/usecase/hardcoded_test.go",
		`package usecase

var hardcoded = map[string]struct{}{"the": {}, "and": {}}
`)
	rep := &report.Report{}
	ScanStopwordMapsInApp(root, nil, rep)
	if len(rep.Violations) != 0 {
		t.Fatalf("test file tripped gate: got %d violations", len(rep.Violations))
	}
}
