// cmd/admin/fullimages_migrate_test.go — hermetic TDD coverage for the
// fullimages video→image migration CLI.
//
// godlike/06 SSOT: each test exercises one invariant of the canonical
// migration table (see fullimages_migrate.go::fullimagesMigratePatterns).
// godlike/07 NO-FAKE-AVAILABILITY: all tests use t.TempDir() to avoid
// any side-effect on the operator's working tree.

package main

import (
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
