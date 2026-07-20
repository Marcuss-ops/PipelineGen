// Package scan — test for ScanNoPipelineMapStr (Step 6
// per-check, typed-carrier purity gate, July 2026).
//
// Hermetic (t.TempDir-anchored). Mirrors the existing
// percheck_pipeline_map_carrier_ban test surface with the
// new rule-family id (`percheck_no_pipeline_mapstr`).
//
//  1. A `map[string]any` typed FIELD inside one of the four
//     pipeline-carrier files trips the gate as SeverityError.
//  2. A `tracker.TrackEvent("...", "...", map[string]any{...})`
//     inline metadata argument is EXEMPT.
//  3. Comment-only matches produce a WARN residue in
//     !productionOnly mode and are silenced in productionOnly
//     mode.
//  4. Out-of-scope files (NOT one of the canonical four) are
//     not scanned.
//  5. In-scope file with a typed non-violation regular field
//     is not flagged.
package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// makeFileForNoPipelineMapStrTest writes a fixture .go file
// at the requested repo-relative path inside `root`.
func makeFileForNoPipelineMapStrTest(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestScanNoPipelineMapStr_TypedFieldTripsGate verifies the
// canonical violation shape (`State map[string]any` as a
// struct field inside one of the four pipeline files).
func TestScanNoPipelineMapStr_TypedFieldTripsGate(t *testing.T) {
	root := t.TempDir()
	makeFileForNoPipelineMapStrTest(t, root, "internal/application/scripts/usecase/generation_postprocess.go",
		`package usecase

type ProcessedGeneration struct {
	State map[string]any // forbidden typed field (Step 6 per-check trip)
}
`)
	rep := &report.Report{}
	ScanNoPipelineMapStr(root, nil, rep, true)
	if len(rep.Violations) == 0 {
		t.Fatalf("expected ≥ 1 violation; got 0 (typed map[string]any field must trip gate)")
	}
	if rep.Violations[0].Rule != noPipelineMapStrRule {
		t.Fatalf("violation rule = %q, want %q", rep.Violations[0].Rule, noPipelineMapStrRule)
	}
	if rep.Violations[0].Severity != string(report.SeverityError) {
		t.Fatalf("violation severity = %q, want SeverityError", rep.Violations[0].Severity)
	}
}

// TestScanNoPipelineMapStr_TrackEventInlineMetadataExempt
// verifies that tracker.TrackEvent inline metadata is allowed.
func TestScanNoPipelineMapStr_TrackEventInlineMetadataExempt(t *testing.T) {
	root := t.TempDir()
	makeFileForNoPipelineMapStrTest(t, root, "internal/application/scripts/usecase/generation_postprocess.go",
		`package usecase

import "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"

type PreparedGeneration struct{}

func Note(p *PreparedGeneration, tracker *usecase.ProgressTracker) {
	tracker.TrackEvent("clips.bound", "Clip bindings applied", map[string]any{
		"item_id":    "x",
		"clip_count": 3,
	})
}
`)
	rep := &report.Report{}
	ScanNoPipelineMapStr(root, nil, rep, true)
	if len(rep.Violations) != 0 {
		t.Fatalf("tracker.TrackEvent inline metadata tripped gate: got %d violations\nfirst: %s",
			len(rep.Violations), rep.Violations[0].Note)
	}
}

// TestScanNoPipelineMapStr_CommentOnlyResidue_Warned
// verifies comment-only matches produce a WARN residue in
// !productionOnly mode.
func TestScanNoPipelineMapStr_CommentOnlyResidue_Warned(t *testing.T) {
	root := t.TempDir()
	makeFileForNoPipelineMapStrTest(t, root, "internal/application/scripts/usecase/generation_prepare.go",
		`package usecase

// the legacy PipelineState map[string]any anti-pattern was banned here in v0.
func Note() {}
`)
	rep := &report.Report{}
	ScanNoPipelineMapStr(root, nil, rep, false)
	if len(rep.Violations) != 0 {
		t.Fatalf("comment-only produced violation: got %d, want 0", len(rep.Violations))
	}
	if !containsString(rep.Warnings, "no-pipeline-mapstr-comments:") {
		t.Fatalf("comment-only did NOT produce WARN residue: %v", rep.Warnings)
	}
}

// TestScanNoPipelineMapStr_ProductionOnlySilencesWarn
// verifies productionOnly=true silences the comment-only
// WARN bucket.
func TestScanNoPipelineMapStr_ProductionOnlySilencesWarn(t *testing.T) {
	root := t.TempDir()
	makeFileForNoPipelineMapStrTest(t, root, "internal/application/scripts/usecase/generation_prepare.go",
		`package usecase

// map[string]any discuss in godoc only.
func Note() {}
`)
	rep := &report.Report{}
	ScanNoPipelineMapStr(root, nil, rep, true)
	for _, w := range rep.Warnings {
		if containsString([]string{w}, "no-pipeline-mapstr-comments:") {
			t.Fatalf("productionOnly did NOT silence comment-only WARN: %s", w)
		}
	}
}

// TestScanNoPipelineMapStr_OutOfScopeNotScanned verifies
// only the four pre-canned target files are scanned; an
// equivalent file in an unrelated directory is exempt.
func TestScanNoPipelineMapStr_OutOfScopeNotScanned(t *testing.T) {
	root := t.TempDir()
	makeFileForNoPipelineMapStrTest(t, root, "internal/application/random_module/random_file.go",
		`package random_module

type X struct {
	State map[string]any
}
`)
	rep := &report.Report{}
	ScanNoPipelineMapStr(root, nil, rep, true)
	if len(rep.Violations) != 0 {
		t.Fatalf("out-of-scope file tripped gate: got %d violations\nfirst: %s",
			len(rep.Violations), rep.Violations[0].Note)
	}
}

// TestScanNoPipelineMapStr_InScopeNonViolationRegularField
// verifies an in-scope file with a typed struct field (no
// `map[string]any`) is NOT flagged.
func TestScanNoPipelineMapStr_InScopeNonViolationRegularField(t *testing.T) {
	root := t.TempDir()
	makeFileForNoPipelineMapStrTest(t, root, "internal/application/scripts/usecase/generation_prepare.go",
		`package usecase

import "time"

type PreparedGeneration struct {
	SourceResolveMs int64
	PlanBuildMs     int64
	At              time.Time
}
`)
	rep := &report.Report{}
	ScanNoPipelineMapStr(root, nil, rep, true)
	if len(rep.Violations) != 0 {
		t.Fatalf("in-scope typed non-violation tripped gate: got %d violations\nfirst: %s",
			len(rep.Violations), rep.Violations[0].Note)
	}
}
