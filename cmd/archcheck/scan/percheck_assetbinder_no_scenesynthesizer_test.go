// Package scan — test for ScanAssetBinderNoSynthesizer
// (PR-DIAGNOSI-FINALE rule 1).
//
// This file is hermetic (t.TempDir-anchored) and validates
// the gate's three core invariants on the canonical binder:
//
//  1. A clean binder emits zero violations.
//  2. A binder containing `synthesizer.NewSceneSynthesizer()`
//     emits a violation.
//  3. A binder importing the canonical synthesizer package emits
//     a violation.
//  4. Comment-only residue emits a single WARN (in
//     !productionOnly mode) and zero violations.
//  5. productionOnly mode silences the comment-only WARN
//     bucket (per PR-P12-PERCHECK-BASELINE-ZERO precedent).
package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// makeBinderFileForSynthesizerTest writes a single .go file
// to <root>/<relPath> with the provided content. Mirrors the
// pattern from percheck_asset_state_canonical_14_test.go.
func makeBinderFileForSynthesizerTest(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestScanAssetBinderNoSynthesizer_CleanBinder verifies that
// a canonical binder file with no SceneSynthesizer reference
// emits zero violations.
func TestScanAssetBinderNoSynthesizer_CleanBinder(t *testing.T) {
	root := t.TempDir()
	makeBinderFileForSynthesizerTest(t, root, "internal/capabilities/scripts/scene/binder.go",
		`package scene
func BindClips() {}
`)
	rep := &report.Report{}
	ScanAssetBinderNoSynthesizer(root, nil, rep, false)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("clean binder tripped gate: got %d violations, want 0\n%s", got, rep.Violations[0].Note)
	}
}

// TestScanAssetBinderNoSynthesizer_SynthesizerCall verifies
// that a binder containing `synthesizer.NewSceneSynthesizer()`
// emits a violation.
func TestScanAssetBinderNoSynthesizer_SynthesizerCall(t *testing.T) {
	root := t.TempDir()
	makeBinderFileForSynthesizerTest(t, root, "internal/capabilities/scripts/scene/binder.go",
		`package scene
import "fmt"
func BindClips() {
	_ = fmt.Sprintf
	_ = "should trip the gate"
	SceneSynthesizer_helper := 1
	_ = SceneSynthesizer_helper
}
`)
	rep := &report.Report{}
	ScanAssetBinderNoSynthesizer(root, nil, rep, false)
	if got := len(rep.Violations); got == 0 {
		t.Fatalf("Synthesizer-named identifier did NOT trip gate; expected ≥ 1 violation")
	}
	if rep.Violations[0].Rule != assetBinderSynthesizerRule {
		t.Fatalf("violation rule = %q, want %q", rep.Violations[0].Rule, assetBinderSynthesizerRule)
	}
}

// TestScanAssetBinderNoSynthesizer_SynthesizerPackageImport
// verifies that importing the canonical synthesizer package
// inside the binder emits a violation.
func TestScanAssetBinderNoSynthesizer_SynthesizerPackageImport(t *testing.T) {
	root := t.TempDir()
	makeBinderFileForSynthesizerTest(t, root, "internal/capabilities/scripts/scene/binder.go",
		`package scene
import _ "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/scene/synthesizer"
func BindClips() {}
`)
	rep := &report.Report{}
	ScanAssetBinderNoSynthesizer(root, nil, rep, false)
	if got := len(rep.Violations); got == 0 {
		t.Fatalf("Synthesizer package import did NOT trip gate; expected ≥ 1 violation")
	}
}

// TestScanAssetBinderNoSynthesizer_CommentOnlyResidue
// verifies that a comment-only reference emits a single WARN
// in non-productionOnly mode.
func TestScanAssetBinderNoSynthesizer_CommentOnlyResidue(t *testing.T) {
	root := t.TempDir()
	makeBinderFileForSynthesizerTest(t, root, "internal/capabilities/scripts/scene/binder.go",
		`package scene
// SceneSynthesizer is documented here.
func BindClips() {}
`)
	rep := &report.Report{}
	ScanAssetBinderNoSynthesizer(root, nil, rep, false)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("comment-only reference produced violation: got %d, want 0\n%s", got, rep.Violations[0].Note)
	}
	if !strings.Contains(strings.Join(rep.Warnings, "|"), "binder-synthesizer-comments:") {
		t.Fatalf("comment-only reference did NOT produce WARN; warnings=%v", rep.Warnings)
	}
}

// TestScanAssetBinderNoSynthesizer_CommentOnlyResidueProductionOnly
// verifies that productionOnly=true silences the comment-only
// WARN bucket.
func TestScanAssetBinderNoSynthesizer_CommentOnlyResidueProductionOnly(t *testing.T) {
	root := t.TempDir()
	makeBinderFileForSynthesizerTest(t, root, "internal/capabilities/scripts/scene/binder.go",
		`package scene
// SceneSynthesizer is documented here.
func BindClips() {}
`)
	rep := &report.Report{}
	ScanAssetBinderNoSynthesizer(root, nil, rep, true)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("comment-only reference produced violation in productionOnly mode: got %d, want 0", got)
	}
	for _, w := range rep.Warnings {
		if strings.Contains(w, "binder-synthesizer-comments:") {
			t.Fatalf("productionOnly mode did NOT silence comment-only WARN: %s", w)
		}
	}
}

// TestScanAssetBinderNoSynthesizer_CanonicalFileUnreadable
// verifies that opening an empty/missing canonical binder
// emits a severity-error violation (fail-closed, godlike/07).
func TestScanAssetBinderNoSynthesizer_CanonicalFileUnreadable(t *testing.T) {
	root := t.TempDir()
	// NO binder.go created — the canonical file is absent.
	rep := &report.Report{}
	ScanAssetBinderNoSynthesizer(root, nil, rep, false)
	if got := len(rep.Violations); got == 0 {
		t.Fatalf("missing canonical binder did NOT trip gate; expected ≥ 1 violation")
	}
	if rep.Violations[0].Rule != assetBinderSynthesizerRule {
		t.Fatalf("violation rule = %q, want %q", rep.Violations[0].Rule, assetBinderSynthesizerRule)
	}
}
