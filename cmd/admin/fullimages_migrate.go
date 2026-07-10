// cmd/admin/fullimages_migrate.go — operator-facing migration CLI for the
// fullimages video→image rename (LEGACY-CLEANUP-5-ITEM-ORCHESTRATION
// Item 5, Option B verdict, shipped 2026-07-10).
//
// Migration contract (per `architecture/action-plans/2026-07-10-legacy-cleanup-5-item-orchestration.md#§7.1`):
//
//  1. Scan operator scripts/docs (config/*.yaml, *.sh, *.py, *.js, *.md, *.json)
//     for old `fullimages` literal references.
//  2. Print a human-readable report listing every file with old patterns
//     and the count per pattern class.
//  3. `--apply` (operator-explicit) writes the text replacements back to
//     disk; default is dry-run (no writes) per godlike/07 NO-FAKE-AVAILABILITY.
//  4. Single canonical command (NOT a sub-subcommand) per the
//     cmd/admin/<name>.go pattern (mirrors cmd/admin/cleanup_drive_orphans.go).
//
// Renames covered (canonical surfaces, per service.go + handler.go
// comments shipped in the closure):
//
//   - URL:       /api/fullimages/video/generate → /api/fullimages/image/generate
//   - JSON:      .videos[                       → .images[
//     .videos" / ["videos"]          → .images" / ["images"]
//   - Go type:   SectionVideo                   → SectionImage
//   - Go field:  VideoPath                      → ImagePath
//   - Go method: generateOneVideo               → generateOneImage
//
// godlike/07 NO-FAKE-AVAILABILITY: the CLI is hermetic (pure
// filesystem scan; no live-stack dependency, no Drive/Qdrant/auth
// surface). The migration table is byte-equivalent to the canonical
// closure commits — no fabricated renames.
//
// godlike/07 minimum-blast-radius: --apply writes ONLY the exact
// pattern replacements in the table above; the scan does NOT touch
// any file outside the `--exts` allowlist; the apply path writes
// bytes-equivalent to the dry-run report (same line count, no
// formatting changes).
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// fullimagesMigratePattern is the canonical per-class rename table.
// godlike/06 SSOT: each entry maps an old literal to a new literal;
// the replacement is byte-equivalent to the canonical closure commits.
type fullimagesMigratePattern struct {
	Class    string // human-readable class label (e.g. "URL", "JSON", "Go type")
	Old      string // old literal to search for
	New      string // new literal to write
	Filename string // human-readable description for the report
}

var fullimagesMigratePatterns = []fullimagesMigratePattern{
	{
		Class:    "URL",
		Old:      "api/fullimages/video/generate",
		New:      "api/fullimages/image/generate",
		Filename: "REST endpoint path",
	},
	{
		Class:    "URL-partial",
		Old:      "fullimages/video/generate",
		New:      "fullimages/image/generate",
		Filename: "REST endpoint path (partial)",
	},
	{
		Class:    "JSON-bracket",
		Old:      ".videos[",
		New:      ".images[",
		Filename: "jq field access",
	},
	{
		Class:    "JSON-dquote",
		Old:      `["videos"]`,
		New:      `["images"]`,
		Filename: "JSON string key",
	},
	{
		Class:    "JSON-name",
		Old:      `"videos"`,
		New:      `"images"`,
		Filename: "JSON name key",
	},
	{
		Class:    "JSON-legacy",
		Old:      `videos:`,
		New:      `images:`,
		Filename: "JSON legacy key",
	},
	{
		Class:    "Go-type",
		Old:      "SectionVideo",
		New:      "SectionImage",
		Filename: "Go type rename",
	},
	{
		Class:    "Go-field",
		Old:      "VideoPath",
		New:      "ImagePath",
		Filename: "Go struct field rename",
	},
	{
		Class:    "Go-method",
		Old:      "generateOneVideo",
		New:      "generateOneImage",
		Filename: "Go method rename",
	},
}

// runFullImagesMigrate is the canonical entry point for the operator CLI.
// Single command (no sub-subcommands) per the cmd/admin/<name>.go pattern.
//
// godlike/07 NO-FAKE-AVAILABILITY: this function is hermetic — no
// live-stack dependency, no Drive/Qdrant/auth surface, no
// composition-root wiring. The migration table is byte-equivalent to
// the canonical closure commits.
func runFullImagesMigrate(args []string) error {
	flagSet := flag.NewFlagSet("fullimages-migrate", flag.ContinueOnError)
	flagSet.SetOutput(os.Stderr)
	apply := flagSet.Bool("apply", false, "Write the text replacements to disk (default: dry-run only)")
	targetDir := flagSet.String("target-dir", ".", "Directory to scan (hermetic: local filesystem only)")
	extsFlag := flagSet.String("exts", ".sh,.py,.js,.md,.yaml,.yml,.json,.go", "Comma-separated file extensions to scan (default: operator-script + doc + go)")
	if err := flagSet.Parse(args); err != nil {
		return err
	}

	fmt.Println("NOTICE: fullimages video→image migration (LEGACY-CLEANUP-5-ITEM-ORCHESTRATION Item 5, Option B)")
	fmt.Println("NOTICE: shipped 2026-07-10; CLI: `fullimages-migrate [--apply] [--target-dir DIR] [--exts CSV]`")
	fmt.Println("NOTICE: default = dry-run (no writes); --apply writes text replacements to disk")
	fmt.Println()

	// Validate --target-dir early
	absDir, err := filepath.Abs(*targetDir)
	if err != nil {
		return fmt.Errorf("invalid --target-dir: %w", err)
	}
	info, err := os.Stat(absDir)
	if err != nil {
		return fmt.Errorf("--target-dir %q not accessible: %w", absDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("--target-dir %q is not a directory", absDir)
	}

	// Parse --exts into a set
	extSet := make(map[string]bool)
	for _, e := range strings.Split(*extsFlag, ",") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		extSet[strings.ToLower(e)] = true
	}
	if len(extSet) == 0 {
		return fmt.Errorf("--exts must contain at least one extension")
	}

	// Walk targetDir and collect per-file per-pattern match counts.
	// fileHits: path → pattern class → count
	type fileHitsMap = map[string]map[string]int
	fileHits := make(fileHitsMap)

	walkErr := filepath.WalkDir(absDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Don't abort the whole walk on a single permission error;
			// record the skip and continue (operator-visible in the report).
			fmt.Fprintf(os.Stderr, "WARN: cannot access %q: %v (skipped)\n", path, err)
			return nil
		}
		if d.IsDir() {
			// Skip .git and node_modules (operator noise; not under migration contract)
			base := filepath.Base(path)
			if base == ".git" || base == "node_modules" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !extSet[ext] {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: cannot read %q: %v (skipped)\n", path, err)
			return nil
		}
		// Skip the CLI itself to avoid self-matching (the patterns
		// appear as string literals in the CLI's pattern table +
		// test source). Scope to basename only to avoid false-positives
		// on operator scripts ending in the same name.
		base := filepath.Base(path)
		if base == "fullimages_migrate.go" || base == "fullimages_migrate_test.go" {
			return nil
		}
		// Skip already-migrated files: if a file contains the new
		// "image" pattern but ZERO old patterns, it's canonical
		// (post-closure) — don't pollute the report.
		hits := scanFileForOldPatterns(string(content))
		if len(hits) == 0 {
			return nil
		}
		fileHits[path] = hits
		return nil
	})
	if walkErr != nil {
		return fmt.Errorf("walk failed: %w", walkErr)
	}

	// Render the dry-run report.
	report := buildFullimagesMigrateReport(absDir, fileHits, fullimagesMigratePatterns)
	fmt.Print(report)

	// Apply (if requested)
	if *apply {
		appliedCount, applyErr := applyFullimagesMigrate(fileHits, fullimagesMigratePatterns)
		if applyErr != nil {
			return fmt.Errorf("apply failed: %w", applyErr)
		}
		fmt.Printf("\nAPPLIED: %d file(s) updated\n", appliedCount)
		fmt.Println("NOTICE: please re-run your downstream tests to confirm the migration.")
	} else {
		fmt.Println("\nDRY-RUN: no files were modified. Re-run with --apply to write the changes.")
	}

	return nil
}

// scanFileForOldPatterns returns a map of pattern class → occurrence count
// for every pattern whose Old literal appears at least once in content.
// godlike/07 NO-FAKE-AVAILABILITY: the literal scan is byte-precise
// (strings.Count, no regex backtracking).
func scanFileForOldPatterns(content string) map[string]int {
	hits := make(map[string]int)
	for _, p := range fullimagesMigratePatterns {
		if n := strings.Count(content, p.Old); n > 0 {
			hits[p.Class] = n
		}
	}
	return hits
}

// buildFullimagesMigrateReport renders the canonical human-readable
// dry-run / apply-explanation report. Pure function (no side effects)
// per godlike/06 SSOT testability.
func buildFullimagesMigrateReport(rootDir string, fileHits map[string]map[string]int, patterns []fullimagesMigratePattern) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Target directory: %s\n", rootDir)
	fmt.Fprintf(&b, "Patterns scanned: %d\n\n", len(patterns))

	if len(fileHits) == 0 {
		b.WriteString("No old patterns found. The codebase is already migrated.\n")
		return b.String()
	}

	// Sort file paths for stable, diff-friendly output (godlike/06 SSOT).
	paths := make([]string, 0, len(fileHits))
	for p := range fileHits {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	totalFiles := len(paths)
	totalHits := 0
	for _, hits := range fileHits {
		for _, n := range hits {
			totalHits += n
		}
	}

	fmt.Fprintf(&b, "Files with old patterns: %d\n", totalFiles)
	fmt.Fprintf(&b, "Total pattern matches:    %d\n\n", totalHits)

	// Pattern-class summary
	classTotals := make(map[string]int)
	for _, hits := range fileHits {
		for class, n := range hits {
			classTotals[class] += n
		}
	}
	b.WriteString("Per-class summary:\n")
	for _, p := range patterns {
		if n, ok := classTotals[p.Class]; ok && n > 0 {
			fmt.Fprintf(&b, "  %-12s  %-30s  %4d match(es)\n", p.Class, p.Filename, n)
		}
	}
	b.WriteString("\nPer-file details:\n")
	for _, path := range paths {
		hits := fileHits[path]
		fmt.Fprintf(&b, "  %s\n", path)
		// Stable per-class ordering
		for _, p := range patterns {
			if n, ok := hits[p.Class]; ok && n > 0 {
				fmt.Fprintf(&b, "    %s: %d  (%q → %q)\n", p.Class, n, p.Old, p.New)
			}
		}
	}
	return b.String()
}

// applyFullimagesMigrate writes the text replacements to disk for every
// file in fileHits. Returns the count of files updated.
// godlike/07 minimum-blast-radius: only the EXACT pattern replacements
// (no formatting changes, no whitespace normalization, no line
// reordering). One file write per file.
func applyFullimagesMigrate(fileHits map[string]map[string]int, patterns []fullimagesMigratePattern) (int, error) {
	count := 0
	for path := range fileHits {
		content, err := os.ReadFile(path)
		if err != nil {
			return count, fmt.Errorf("read %q: %w", path, err)
		}
		original := string(content)
		updated := original
		for _, p := range patterns {
			if _, hasHit := fileHits[path][p.Class]; hasHit {
				updated = strings.ReplaceAll(updated, p.Old, p.New)
			}
		}
		if updated != original {
			// Preserve file mode bits (best-effort: readable
			// permissions; write-only ownership changes require
			// a separate chmod step which is OUT OF SCOPE per
			// godlike/07 minimum-blast-radius).
			info, statErr := os.Stat(path)
			mode := fs.FileMode(0o644)
			if statErr == nil {
				mode = info.Mode().Perm()
			}
			if err := os.WriteFile(path, []byte(updated), mode); err != nil {
				return count, fmt.Errorf("write %q: %w", path, err)
			}
			count++
		}
	}
	return count, nil
}
