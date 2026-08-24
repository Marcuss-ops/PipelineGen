// Package scan — test for ScanStopwordMapsInApp
// (percheck_stopword_maps_in_app forward-prevention gate).
//
// Hermetic (t.TempDir-anchored). Validates the core invariants of the
// hardcoded-stop-word-map gate:
//
//  1. A production file under internal/application/ containing a
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
//  5. Files outside internal/application|infrastructure are out of
//     scope (pkg/ and internal/domain/linguistics/ — the canonical
//     lexicon home — are never scanned).
//  6. Test files are exempt (regression-guard surface).
package governance

import (
	"os"
	"path/filepath"
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
	makeStopwordFixture(t, root, "internal/application/scripts/usecase/hardcoded.go",
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
	makeStopwordFixture(t, root, "internal/application/scripts/usecase/hardcoded.go",
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
	makeStopwordFixture(t, root, "internal/application/scripts/usecase/hardcoded.go",
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
	makeStopwordFixture(t, root, "internal/application/config/set.go",
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
// lexicon home (internal/domain/linguistics/) is never scanned — the
// gate's own doc-comment declares it the legitimate owner of linguistic
// data.
func TestScanStopwordMapsInApp_CanonicalLexiconExempt(t *testing.T) {
	root := t.TempDir()
	makeStopwordFixture(t, root, "internal/domain/linguistics/lexicon_builtin.go",
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

// TestScanStopwordMapsInApp_TestFileExempt verifies _test.go files are
// exempt (regression-guard surface).
func TestScanStopwordMapsInApp_TestFileExempt(t *testing.T) {
	root := t.TempDir()
	makeStopwordFixture(t, root, "internal/application/scripts/usecase/hardcoded_test.go",
		`package usecase

var hardcoded = map[string]struct{}{"the": {}, "and": {}}
`)
	rep := &report.Report{}
	ScanStopwordMapsInApp(root, nil, rep)
	if len(rep.Violations) != 0 {
		t.Fatalf("test file tripped gate: got %d violations", len(rep.Violations))
	}
}
