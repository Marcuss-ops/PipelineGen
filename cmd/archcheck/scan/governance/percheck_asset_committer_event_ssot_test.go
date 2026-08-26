// Package scan — test for ScanAssetCommitterEventSSOT
// (PR-DIAGNOSI-FINALE rule 3).
//
// Hermetic (t.TempDir-anchored). Validates the four core
// invariants of the AssetCommitter-event SSOT gate:
//
//  1. The canonical AssetCommitter emitting the literal is
//     EXEMPT (no violation).
//  2. A non-canonical producer emitting the literal trips
//     the gate as SeverityError.
//  3. Test fixtures are exempt.
//  4. Comment-only references are residue-accounted (WARN in
//     !productionOnly mode; silenced in productionOnly mode).
//  5. The literal .v1 envelope form is also caught.
package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func makeFileForCommitterEventTest(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestScanAssetCommitterEventSSOT_CanonicalExempt verifies
// the canonical AssetCommitter emitting the literal is
// EXEMPT (zero violations).
func TestScanAssetCommitterEventSSOT_CanonicalExempt(t *testing.T) {
	root := t.TempDir()
	// The canonical AssetCommitter emitting the literal.
	makeFileForCommitterEventTest(t, root, "internal/capabilities/assets/persistence/committer.go",
		`package persistence
const AssetIndexRequestedLiteral = "asset.index.requested"
func CommitAsset() {}
`)
	rep := &report.Report{}
	ScanAssetCommitterEventSSOT(root, nil, rep, true)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("canonical exempt trips gate: got %d violations\nfirst: %s",
			got, rep.Violations[0].Note)
	}
}

// TestScanAssetCommitterEventSSOT_NonCanonicalProducer
// verifies that a non-canonical producer emitting the
// literal trips the gate.
func TestScanAssetCommitterEventSSOT_NonCanonicalProducer(t *testing.T) {
	root := t.TempDir()
	makeFileForCommitterEventTest(t, root, "internal/capabilities/random_other/ad_hoc_enqueuer.go",
		`package random_other
import "fmt"
func BadProducer() {
	fmt.Println("asset.index.requested")
}
`)
	rep := &report.Report{}
	ScanAssetCommitterEventSSOT(root, nil, rep, true)
	if got := len(rep.Violations); got == 0 {
		t.Fatalf("non-canonical producer did NOT trip gate; expected ≥ 1 violation")
	}
	if rep.Violations[0].Rule != assetCommitterEventSSOTRule {
		t.Fatalf("violation rule = %q, want %q", rep.Violations[0].Rule, assetCommitterEventSSOTRule)
	}
}

// TestScanAssetCommitterEventSSOT_V1EnvelopeForm verifies
// that the .v1 envelope form is also caught.
func TestScanAssetCommitterEventSSOT_V1EnvelopeForm(t *testing.T) {
	root := t.TempDir()
	makeFileForCommitterEventTest(t, root, "internal/capabilities/random_other/v1_emitter.go",
		`package random_other
import "fmt"
func BadProducer() {
	fmt.Println("asset.index.requested.v1")
}
`)
	rep := &report.Report{}
	ScanAssetCommitterEventSSOT(root, nil, rep, true)
	if got := len(rep.Violations); got == 0 {
		t.Fatalf("v1 envelope literal did NOT trip gate; expected ≥ 1 violation")
	}
}

// TestScanAssetCommitterEventSSOT_TestFilesExempt verifies
// that __test.go files are exempt (regression-guard surface).
func TestScanAssetCommitterEventSSOT_TestFilesExempt(t *testing.T) {
	root := t.TempDir()
	makeFileForCommitterEventTest(t, root, "internal/capabilities/random_other/test_event_test.go",
		`package random_other
import "testing"
func TestEventLiteral(t *testing.T) {
	_ = "asset.index.requested"
}
`)
	rep := &report.Report{}
	ScanAssetCommitterEventSSOT(root, nil, rep, true)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("test file tripped gate: got %d violations\nfirst: %s",
			got, rep.Violations[0].Note)
	}
}

// TestScanAssetCommitterEventSSOT_FixtureExempt verifies
// tests/ folder is exempt.
func TestScanAssetCommitterEventSSOT_FixtureExempt(t *testing.T) {
	root := t.TempDir()
	makeFileForCommitterEventTest(t, root, "tests/e2e/event_fixture.go",
		`package e2e
import "fmt"
func Fixture() {
	fmt.Println("asset.index.requested")
}
`)
	rep := &report.Report{}
	ScanAssetCommitterEventSSOT(root, nil, rep, true)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("tests/ folder tripped gate: got %d violations\nfirst: %s",
			got, rep.Violations[0].Note)
	}
}

// TestScanAssetCommitterEventSSOT_CommentOnlyResidue verifies
// comment-only references emit a single WARN bucket in
// !productionOnly mode.
func TestScanAssetCommitterEventSSOT_CommentOnlyResidue(t *testing.T) {
	root := t.TempDir()
	makeFileForCommitterEventTest(t, root, "internal/capabilities/random_other/docs_only.go",
		`package random_other
// "asset.index.requested" is the canonical envelope literal.
func Note() {}
`)
	rep := &report.Report{}
	ScanAssetCommitterEventSSOT(root, nil, rep, false)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("comment-only reference produced violation: got %d, want 0", got)
	}
	if !containsString(rep.Warnings, "index-event-comments:") {
		t.Fatalf("comment-only reference did NOT produce WARN: %v", rep.Warnings)
	}
}

// TestScanAssetCommitterEventSSOT_ProductionOnlySilencesWarn
// verifies that productionOnly=true silences the comment-only
// WARN bucket (operator-facing zero-hits claim).
func TestScanAssetCommitterEventSSOT_ProductionOnlySilencesWarn(t *testing.T) {
	root := t.TempDir()
	makeFileForCommitterEventTest(t, root, "internal/capabilities/random_other/docs_only.go",
		`package random_other
// "asset.index.requested" is the canonical envelope literal.
func Note() {}
`)
	rep := &report.Report{}
	ScanAssetCommitterEventSSOT(root, nil, rep, true)
	for _, w := range rep.Warnings {
		if containsString([]string{w}, "index-event-comments:") {
			t.Fatalf("productionOnly mode did NOT silence comment-only WARN: %s", w)
		}
	}
}

// containsString is a tiny helper for test convenience.
func containsString(haystacks []string, needle string) bool {
	for _, h := range haystacks {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}
