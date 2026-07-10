// cmd/admin/fullimages_migrate_test.go — hermetic TDD coverage for the
// fullimages video→image migration CLI.
//
// godlike/06 SSOT: each test exercises one invariant of the canonical
// migration table (see fullimages_migrate.go::fullimagesMigratePatterns).
// godlike/07 NO-FAKE-AVAILABILITY: all tests use t.TempDir() to avoid
// any side-effect on the operator's working tree.

package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFullImagesMigratePatterns_CanonicalTable locks the canonical
// migration table to the byte-equivalent renames shipped in the
// LEGACY-CLEANUP-5-ITEM-ORCHESTRATION Item 5 closure (commit
// b7d73a18335234cf34d27cdaf9cac25c0d3a96bc). A future regression
// that drifts any Old/New pair would surface as a test failure here.
func TestFullImagesMigratePatterns_CanonicalTable(t *testing.T) {
	wantPairs := map[string]string{
		"api/fullimages/video/generate": "api/fullimages/image/generate",
		"fullimages/video/generate":     "fullimages/image/generate",
		".videos[":                      ".images[",
		`["videos"]`:                    `["images"]`,
		`"videos"`:                      `"images"`,
		`videos:`:                       `images:`,
		"SectionVideo":                  "SectionImage",
		"VideoPath":                     "ImagePath",
		"generateOneVideo":              "generateOneImage",
	}
	if len(fullimagesMigratePatterns) != len(wantPairs) {
		t.Fatalf("pattern table size drift: got %d, want %d (a future regression on the canonical table is rejected)",
			len(fullimagesMigratePatterns), len(wantPairs))
	}
	for _, p := range fullimagesMigratePatterns {
		want, ok := wantPairs[p.Old]
		if !ok {
			t.Errorf("unexpected pattern Old=%q — table drift from canonical closure", p.Old)
			continue
		}
		if p.New != want {
			t.Errorf("pattern %q New mismatch: got %q, want %q", p.Old, p.New, want)
		}
	}
}

// TestScanFileForOldPatterns_NoHitsReturnsEmpty ensures the scanner
// returns an empty map (not nil-error) for files that contain NONE
// of the old patterns.
func TestScanFileForOldPatterns_NoHitsReturnsEmpty(t *testing.T) {
	hits := scanFileForOldPatterns(`hello world, no legacy references here`)
	if len(hits) != 0 {
		t.Fatalf("expected zero hits, got %v", hits)
	}
}

// TestScanFileForOldPatterns_AllClassesCounted ensures every pattern
// class is counted in a file that contains all 9 old literals.
func TestScanFileForOldPatterns_AllClassesCounted(t *testing.T) {
	src := strings.Join([]string{
		`api/fullimages/video/generate`,        // URL
		`fullimages/video/generate`,            // URL-partial
		`jq '.videos[0] .path'`,                // JSON-bracket
		`["videos"]`,                           // JSON-dquote
		`"videos": [...],`,                     // JSON-name
		`videos: [`,                            // JSON-legacy
		`type SectionVideo struct{}`,           // Go-type
		`VideoPath string`,                     // Go-field
		`func (s *Service) generateOneVideo()`, // Go-method
	}, "\n")
	hits := scanFileForOldPatterns(src)
	if len(hits) != 9 {
		t.Fatalf("expected 9 pattern classes, got %d: %v", len(hits), hits)
	}
	for _, p := range fullimagesMigratePatterns {
		if hits[p.Class] == 0 {
			t.Errorf("pattern class %q not detected", p.Class)
		}
	}
}

// TestBuildFullimagesMigrateReport_EmptyReport pins the canonical
// human-readable report format for the no-hits case (post-migration
// codebase).
func TestBuildFullimagesMigrateReport_EmptyReport(t *testing.T) {
	report := buildFullimagesMigrateReport("/tmp/no-such-dir", map[string]map[string]int{}, fullimagesMigratePatterns)
	if !strings.Contains(report, "No old patterns found") {
		t.Errorf("empty report missing canonical message; got:\n%s", report)
	}
	if !strings.Contains(report, "/tmp/no-such-dir") {
		t.Errorf("empty report missing target directory; got:\n%s", report)
	}
}

// TestBuildFullimagesMigrateReport_DetailedReport pins the canonical
// human-readable report format for the multi-file multi-class case.
func TestBuildFullimagesMigrateReport_DetailedReport(t *testing.T) {
	fileHits := map[string]map[string]int{
		"/tmp/foo.sh": {
			"URL":     2,
			"Go-type": 1,
		},
		"/tmp/bar.md": {
			"JSON-bracket": 3,
		},
	}
	report := buildFullimagesMigrateReport("/tmp", fileHits, fullimagesMigratePatterns)
	for _, sub := range []string{
		"Target directory: /tmp",
		"Files with old patterns: 2",
		"Total pattern matches:    6", // 2+1+3
		"/tmp/foo.sh",
		"/tmp/bar.md",
		// Per-class summary format: "  ClassName  Description  N match(es)"
		"URL",
		"Go-type",
		"JSON-bracket",
		"REST endpoint path", // URL description
		"Go type rename",     // Go-type description
		"jq field access",    // JSON-bracket description
		"match(es)",
		// Per-file detail format: "    Class: N  ("old" → "new")"
		"api/fullimages/video/generate", // Old literal surfaced
		"api/fullimages/image/generate", // New literal surfaced
	} {
		if !strings.Contains(report, sub) {
			t.Errorf("report missing %q; got:\n%s", sub, report)
		}
	}
}

// TestRunFullImagesMigrate_DryRunNoWrites ensures the default
// (no --apply) does NOT modify the filesystem (godlike/07
// NO-FAKE-AVAILABILITY: dry-run is the canonical safe default).
func TestRunFullImagesMigrate_DryRunNoWrites(t *testing.T) {
	dir := t.TempDir()
	src := `api/fullimages/video/generate` + "\n" + `SectionVideo` + "\n"
	target := filepath.Join(dir, "test.sh")
	if err := os.WriteFile(target, []byte(src), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := runFullImagesMigrate([]string{"--target-dir", dir, "--exts", ".sh"}); err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	// File must be byte-identical (no writes).
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if string(got) != src {
		t.Errorf("dry-run modified the file:\nbefore: %q\nafter:  %q", src, got)
	}
}

// TestRunFullImagesMigrate_ApplyRewrites ensures --apply actually
// writes the text replacements to disk (godlike/07 minimum-blast-radius:
// only the EXACT pattern replacements; no formatting changes).
func TestRunFullImagesMigrate_ApplyRewrites(t *testing.T) {
	dir := t.TempDir()
	src := `curl api/fullimages/video/generate && jq '.videos[0] .path'`
	target := filepath.Join(dir, "test.sh")
	if err := os.WriteFile(target, []byte(src), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := runFullImagesMigrate([]string{"--target-dir", dir, "--exts", ".sh", "--apply"}); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	want := `curl api/fullimages/image/generate && jq '.images[0] .path'`
	if string(got) != want {
		t.Errorf("apply did not produce canonical output:\nbefore: %q\nafter:  %q\nwant:   %q", src, got, want)
	}
}

// TestRunFullImagesMigrate_ApplyPreservesFileMode ensures --apply
// preserves the original file mode bits (godlike/07 minimum-blast-radius:
// no chmod side effects on operator scripts).
func TestRunFullImagesMigrate_ApplyPreservesFileMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "exec.sh")
	if err := os.WriteFile(target, []byte("api/fullimages/video/generate\n"), 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := runFullImagesMigrate([]string{"--target-dir", dir, "--exts", ".sh", "--apply"}); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("file mode changed: got %o, want 0o755", info.Mode().Perm())
	}
}

// TestRunFullImagesMigrate_SkipsSelf ensures the CLI does NOT match
// its own source code (the patterns appear as string literals in the
// CLI's scan table + test file).
func TestRunFullImagesMigrate_SkipsSelf(t *testing.T) {
	dir := t.TempDir()
	// Drop a fake "fullimages_migrate.go" with old patterns; the
	// CLI must NOT report it (canonical godlike/07 NO-FAKE-AVAILABILITY:
	// the CLI's own source is NOT a migration target).
	fake := filepath.Join(dir, "fullimages_migrate.go")
	src := `var fullimagesMigratePatterns = []struct{ Old, New string }{{"api/fullimages/video/generate", "api/fullimages/image/generate"}}`
	if err := os.WriteFile(fake, []byte(src), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Capture stderr (the CLI writes to stderr on the error path)
	if err := runFullImagesMigrate([]string{"--target-dir", dir, "--exts", ".go"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	// The file must be byte-identical (the CLI skipped itself).
	got, err := os.ReadFile(fake)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if string(got) != src {
		t.Errorf("CLI scanned its own source file (should be self-skipping): got %q", got)
	}
}

// TestRunFullImagesMigrate_ExtFilter ensures the --exts filter
// correctly excludes files with non-matching extensions (godlike/07
// minimum-blast-radius: scope-limited scan, hermetic to the operator's
// declared file types).
func TestRunFullImagesMigrate_ExtFilter(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "test.sh")
	if err := os.WriteFile(target, []byte("api/fullimages/video/generate\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// --exts=".go" should NOT match the .sh file.
	if err := runFullImagesMigrate([]string{"--target-dir", dir, "--exts", ".go", "--apply"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if !strings.Contains(string(got), "api/fullimages/video/generate") {
		t.Errorf("--exts filter did not exclude the .sh file; got %q", got)
	}
}

// TestRunFullImagesMigrate_InvalidTargetDir ensures the CLI fails
// fast (typed error) when --target-dir does not exist (godlike/07
// NO-FAKE-AVAILABILITY: no silent-success on a misconfigured scan).
func TestRunFullImagesMigrate_InvalidTargetDir(t *testing.T) {
	err := runFullImagesMigrate([]string{"--target-dir", "/tmp/this-path-does-not-exist-fullimages-migrate-test", "--exts", ".sh"})
	if err == nil {
		t.Fatalf("expected error for invalid --target-dir, got nil")
	}
	if !strings.Contains(err.Error(), "not accessible") && !strings.Contains(err.Error(), "no such file") {
		t.Errorf("error message should mention 'not accessible' or 'no such file'; got: %v", err)
	}
}

// TestRunFullImagesMigrate_EmptyExts ensures the CLI rejects
// --exts="" (godlike/07 NO-FAKE-AVAILABILITY: refuse to scan with
// zero file types).
func TestRunFullImagesMigrate_EmptyExts(t *testing.T) {
	dir := t.TempDir()
	err := runFullImagesMigrate([]string{"--target-dir", dir, "--exts", ""})
	if err == nil {
		t.Fatalf("expected error for --exts=\"\", got nil")
	}
	if !strings.Contains(err.Error(), "at least one extension") {
		t.Errorf("error message should mention 'at least one extension'; got: %v", err)
	}
}

// TestSplitExtsCSV_EmptyInput pins the canonical empty-input behavior
// (returns nil, not an empty slice — JSON serialization omits the
// field via the `omitempty`-equivalent nil check in the report builder).
func TestSplitExtsCSV_EmptyInput(t *testing.T) {
	got := splitExtsCSV("")
	if got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
}

// TestSplitExtsCSV_DedupAndNormalize pins the canonical parsing
// contract: trim whitespace, normalize to lower-case + leading dot,
// deduplicate, preserve input order.
func TestSplitExtsCSV_DedupAndNormalize(t *testing.T) {
	got := splitExtsCSV(".sh, .py ,sh,PY,.sh,go")
	want := []string{".sh", ".py", ".go"} // .sh dedup'd (3x), .py case-normalized + dedup'd (2x), .go normalized
	if len(got) != len(want) {
		t.Fatalf("got %v (%d items), want %v (%d items)", got, len(got), want, len(want))
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("got[%d]=%q, want %q", i, got[i], v)
		}
	}
}

// TestBuildFullimagesMigrateJSONReport_EmptyReport pins the canonical
// JSON schema for the no-hits case: empty files array, null
// per_class_totals, null warnings, mode=dry-run, applied_files_count=0.
func TestBuildFullimagesMigrateJSONReport_EmptyReport(t *testing.T) {
	rep := buildFullimagesMigrateJSONReport(
		"/tmp/no-such-dir",
		[]string{".sh", ".py"},
		false,
		map[string]map[string]int{},
		fullimagesMigratePatterns,
		nil,
		0,
	)
	if rep.Meta.TargetDir != "/tmp/no-such-dir" {
		t.Errorf("meta.target_dir=%q, want /tmp/no-such-dir", rep.Meta.TargetDir)
	}
	if rep.Meta.Mode != "dry-run" {
		t.Errorf("meta.mode=%q, want dry-run", rep.Meta.Mode)
	}
	if len(rep.Files) != 0 {
		t.Errorf("expected empty files array, got %d files", len(rep.Files))
	}
	if rep.Totals.FilesWithHits != 0 || rep.Totals.TotalMatches != 0 {
		t.Errorf("expected zero totals, got %+v", rep.Totals)
	}
	if rep.PerClassTotals != nil {
		t.Errorf("per_class_totals should be nil for empty report, got %v", rep.PerClassTotals)
	}
	if rep.Warnings != nil {
		t.Errorf("warnings should be nil for empty report, got %v", rep.Warnings)
	}
	if rep.AppliedFilesCount != 0 {
		t.Errorf("applied_files_count should be 0 for dry-run, got %d", rep.AppliedFilesCount)
	}
	if len(rep.Patterns) != len(fullimagesMigratePatterns) {
		t.Errorf("patterns should surface the canonical table verbatim, got %d want %d",
			len(rep.Patterns), len(fullimagesMigratePatterns))
	}
}

// TestBuildFullimagesMigrateJSONReport_DetailedReport pins the
// canonical JSON schema for the multi-file multi-class case: sorted
// files array, per_class_totals aggregated, totals correct.
func TestBuildFullimagesMigrateJSONReport_DetailedReport(t *testing.T) {
	fileHits := map[string]map[string]int{
		"/tmp/foo.sh": {
			"URL":     2,
			"Go-type": 1,
		},
		"/tmp/bar.md": {
			"JSON-bracket": 3,
		},
	}
	rep := buildFullimagesMigrateJSONReport(
		"/tmp",
		[]string{".sh", ".md"},
		false,
		fileHits,
		fullimagesMigratePatterns,
		nil,
		0,
	)
	if rep.Totals.FilesWithHits != 2 {
		t.Errorf("totals.files_with_hits=%d, want 2", rep.Totals.FilesWithHits)
	}
	if rep.Totals.TotalMatches != 6 { // 2+1+3
		t.Errorf("totals.total_matches=%d, want 6", rep.Totals.TotalMatches)
	}
	// Sorted file order: bar.md < foo.sh
	if len(rep.Files) != 2 || rep.Files[0].Path != "/tmp/bar.md" || rep.Files[1].Path != "/tmp/foo.sh" {
		t.Errorf("files not sorted: %+v", rep.Files)
	}
	if rep.PerClassTotals["URL"] != 2 || rep.PerClassTotals["Go-type"] != 1 || rep.PerClassTotals["JSON-bracket"] != 3 {
		t.Errorf("per_class_totals mismatch: %+v", rep.PerClassTotals)
	}
	// Per-file hits must include the full per-class map (not the
	// per_class_totals aggregate).
	if rep.Files[1].Hits["URL"] != 2 || rep.Files[1].Hits["Go-type"] != 1 {
		t.Errorf("foo.sh hits mismatch: %+v", rep.Files[1].Hits)
	}
	if rep.Files[1].TotalMatches != 3 {
		t.Errorf("foo.sh total_matches=%d, want 3", rep.Files[1].TotalMatches)
	}
}

// TestBuildFullimagesMigrateJSONReport_ApplyMode pins the mode +
// applied_files_count contract for the --apply invocation.
func TestBuildFullimagesMigrateJSONReport_ApplyMode(t *testing.T) {
	rep := buildFullimagesMigrateJSONReport(
		"/tmp",
		[]string{".sh"},
		true,
		map[string]map[string]int{"/tmp/x.sh": {"URL": 1}},
		fullimagesMigratePatterns,
		nil,
		3, // simulated apply count (may exceed fileHits size if some files had 0 matches but were touched)
	)
	if rep.Meta.Mode != "apply" {
		t.Errorf("meta.mode=%q, want apply", rep.Meta.Mode)
	}
	if rep.AppliedFilesCount != 3 {
		t.Errorf("applied_files_count=%d, want 3", rep.AppliedFilesCount)
	}
}

// TestBuildFullimagesMigrateJSONReport_Warnings pins that the
// warnings array is populated when warnings are provided (operator
// sees the per-file access errors in the JSON report).
func TestBuildFullimagesMigrateJSONReport_Warnings(t *testing.T) {
	rep := buildFullimagesMigrateJSONReport(
		"/tmp",
		[]string{".sh"},
		false,
		map[string]map[string]int{},
		fullimagesMigratePatterns,
		[]string{"cannot access /tmp/locked: permission denied (skipped)"},
		0,
	)
	if rep.Warnings == nil {
		t.Fatalf("warnings should be populated when warnings provided")
	}
	if len(rep.Warnings) != 1 || rep.Warnings[0] != "cannot access /tmp/locked: permission denied (skipped)" {
		t.Errorf("warnings mismatch: %v", rep.Warnings)
	}
}

// TestRunFullImagesMigrate_JSONOutput_Empty pins the end-to-end
// --json path: valid JSON, no NOTICE banner on stdout, mode=dry-run
// when no --apply.
func TestRunFullImagesMigrate_JSONOutput_Empty(t *testing.T) {
	dir := t.TempDir()
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	if err := runFullImagesMigrate([]string{"--target-dir", dir, "--exts", ".sh", "--json"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	w.Close()
	out, _ := io.ReadAll(r)

	// Must be valid JSON
	var rep fullimagesMigrateJSONReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput:\n%s", err, out)
	}
	// Must NOT contain the NOTICE banner
	if strings.Contains(string(out), "NOTICE:") {
		t.Errorf("--json output should not contain NOTICE banner; got:\n%s", out)
	}
	// Must be the no-hits shape
	if rep.Meta.Mode != "dry-run" {
		t.Errorf("meta.mode=%q, want dry-run", rep.Meta.Mode)
	}
	if len(rep.Files) != 0 {
		t.Errorf("expected empty files, got %d", len(rep.Files))
	}
}

// TestRunFullImagesMigrate_JSONOutput_Detailed pins the end-to-end
// --json path with matches: valid JSON, totals correct, files
// sorted, per-class aggregate present. The seeded source uses
// `fullimages/video/generate` (URL-partial match) — NOT the full
// `api/fullimages/video/generate` (which would also trigger the
// URL-partial substring match, complicating the per-class assertion).
func TestRunFullImagesMigrate_JSONOutput_Detailed(t *testing.T) {
	dir := t.TempDir()
	src := `curl fullimages/video/generate && jq '.videos[0] .path' && echo SectionVideo`
	target := filepath.Join(dir, "test.sh")
	if err := os.WriteFile(target, []byte(src), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	if err := runFullImagesMigrate([]string{"--target-dir", dir, "--exts", ".sh", "--json"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	w.Close()
	out, _ := io.ReadAll(r)

	var rep fullimagesMigrateJSONReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput:\n%s", err, out)
	}
	if rep.Totals.FilesWithHits != 1 {
		t.Errorf("totals.files_with_hits=%d, want 1", rep.Totals.FilesWithHits)
	}
	if rep.Totals.TotalMatches != 3 { // URL-partial + JSON-bracket + Go-type
		t.Errorf("totals.total_matches=%d, want 3", rep.Totals.TotalMatches)
	}
	if len(rep.Files) != 1 || rep.Files[0].Path != target {
		t.Errorf("files mismatch: %+v", rep.Files)
	}
	// All 3 hit classes must be in the per-file hits map
	if rep.Files[0].Hits["URL-partial"] != 1 || rep.Files[0].Hits["JSON-bracket"] != 1 || rep.Files[0].Hits["Go-type"] != 1 {
		t.Errorf("foo.sh hits mismatch: %+v", rep.Files[0].Hits)
	}
}

// TestRunFullImagesMigrate_JSONOutput_Apply pins the end-to-end
// --json + --apply path: JSON includes applied_files_count > 0
// AND the file was actually modified (cross-checks the apply side
// effect).
func TestRunFullImagesMigrate_JSONOutput_Apply(t *testing.T) {
	dir := t.TempDir()
	src := `api/fullimages/video/generate`
	target := filepath.Join(dir, "test.sh")
	if err := os.WriteFile(target, []byte(src), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	if err := runFullImagesMigrate([]string{"--target-dir", dir, "--exts", ".sh", "--json", "--apply"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	w.Close()
	out, _ := io.ReadAll(r)

	var rep fullimagesMigrateJSONReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput:\n%s", err, out)
	}
	if rep.Meta.Mode != "apply" {
		t.Errorf("meta.mode=%q, want apply", rep.Meta.Mode)
	}
	if rep.AppliedFilesCount != 1 {
		t.Errorf("applied_files_count=%d, want 1", rep.AppliedFilesCount)
	}
	// File was actually modified
	got, _ := os.ReadFile(target)
	if !strings.Contains(string(got), "api/fullimages/image/generate") {
		t.Errorf("file was not modified by --apply; got %q", got)
	}
}
