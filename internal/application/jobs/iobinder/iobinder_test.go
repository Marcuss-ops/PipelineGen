package iobinder

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// disallowedPatterns is the canonical set of sync-IO patterns the spec
// bans in hot paths of internal/application/jobs/ per
// PR-REFACTOR-P2-BLOCKING-IO. The 2nd value is a human-readable
// description of why the pattern is disallowed (lift to eager-load at
// boot via injected I/O binder per Pattern 0, godlike/06 SSOT).
//
// The spec's verification command is `rg 'os\.ReadFile|os\.Open'` (a
// substring match that catches `os.Open` AND `os.OpenFile`); this
// test mirrors that scope with 3 separate patterns to keep the
// per-pattern diagnostics clear.
var disallowedPatterns = []struct {
	pattern     *regexp.Regexp
	description string
}{
	{regexp.MustCompile(`\bos\.ReadFile\b`), "sync file read; lift to eager-load at boot via I/O binder (Pattern 0)"},
	{regexp.MustCompile(`\bos\.OpenFile\b`), "sync file open (writeable); lift to eager-load at boot via I/O binder (Pattern 0)"},
	{regexp.MustCompile(`\bos\.Open\b`), "sync file open (readable); lift to eager-load at boot via I/O binder (Pattern 0)"},
}

// exceptionList is the canonical baseline of known sync-IO violations
// in internal/application/jobs/ on origin/main at audit time. The
// keys are `path:line` (filepath.Rel from the package root,
// forward-slash). The values are `true` to keep the map literal
// aligned.
//
// Canonical baseline at audit time (PR-REFACTOR-P2-BLOCKING-IO, 2026-08-08):
//
//	os.ReadFile  hits: 0
//	os.OpenFile  hits: 0
//	os.Open      hits: 1 (internal/application/jobs/assets/service.go:83,
//	                    inside Service.Download — per-asset file open,
//	                    NOT cacheable at boot, NOT in the spec's "lift
//	                    to eager-load" scope because the file path is
//	                    dynamic per assetID and only known at request time)
//
// When a future sub-PR migrates a hit to a typed port, remove the entry
// from this map AND ship a benchmark proving the migration (see
// benchmark_test.go for the canonical before/after template).
var exceptionList = map[string]bool{
	"assets/service.go:100": true, // os.Open in Service.Download — per-asset dynamic local file path; streamed to caller, not cacheable at boot (PR-IOBINDER-P2-DOWNLOAD)
	"assets/service.go:312": true, // os.Open in Service.fetch — opens staged source file after SourceStager.StageSourceV2; path is dynamic per URL, streamed to caller (PR-IOBINDER-P2-DOWNLOAD)
}

// TestNoDirectSyncIOInJobsHotPaths walks internal/application/jobs/
// recursively and fails if any disallowed pattern appears at a
// file:line that is NOT in the exceptionList.
//
// Forward-prevention guarantees:
//  1. Any new sync-IO call site in a production hot path surfaces as
//     a test failure (operator must add to exceptionList with a
//     justification comment, OR migrate the call to a typed I/O binder).
//  2. _test.go files are EXCLUDED via the per-file suffix check.
//  3. Go comment lines are EXCLUDED — the patterns may legitimately
//     appear in godoc/SDK references in production files (e.g. a
//     doc.go that documents the migration path).
func TestNoDirectSyncIOInJobsHotPaths(t *testing.T) {
	packageRoot, err := findJobsPackageRoot()
	if err != nil {
		t.Fatalf("find package root: %v", err)
	}

	type violation struct {
		path        string
		line        int
		content     string
		patternDesc string
	}
	var violations []violation

	// Track which exceptionList entries are actually matched by a hit.
	// Entries that are not matched surface as "stale" (informational).
	exceptionHits := make(map[string]bool, len(exceptionList))
	for k := range exceptionList {
		exceptionHits[k] = false
	}

	for _, dp := range disallowedPatterns {
		err := filepath.WalkDir(packageRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// Skip vendor-style dirs.
				name := d.Name()
				if name == "node_modules" || name == "vendor" || name == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			// Only scan .go production files (skip _test.go per the spec's
			// verification, which explicitly uses `rg -g '!*_test.go'`).
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()

			scanner := bufio.NewScanner(f)
			lineNum := 0
			for scanner.Scan() {
				lineNum++
				line := scanner.Text()
				// Skip Go comment lines — the patterns may legitimately
				// appear in godoc + SDK reference comments. Inline comments
				// (code + `// foo`) still match because the code prefix is
				// non-comment.
				if isGoCommentLine(line) {
					continue
				}
				if !dp.pattern.MatchString(line) {
					continue
				}
				rel, relErr := filepath.Rel(packageRoot, path)
				if relErr != nil {
					return relErr
				}
				key := filepath.ToSlash(rel) + ":" + strconv.Itoa(lineNum)
				if exceptionList[key] {
					exceptionHits[key] = true
					continue
				}
				violations = append(violations, violation{
					path:        rel,
					line:        lineNum,
					content:     strings.TrimSpace(line),
					patternDesc: dp.description,
				})
			}
			return scanner.Err()
		})
		if err != nil {
			t.Fatalf("walk %s: %v", packageRoot, err)
		}
	}

	// Informational: report exceptionList entries that are no longer
	// matched (future agents should remove stale entries).
	for key, seen := range exceptionHits {
		if !seen {
			t.Logf("stale exceptionList entry (no longer matched): %s — safe to remove", key)
		}
	}

	if len(violations) > 0 {
		t.Fatalf("found %d sync-IO violations in internal/application/jobs/ hot paths (not in exceptionList):", len(violations))
		for _, v := range violations {
			t.Errorf("  %s:%d  %s  [%s]", v.path, v.line, v.content, v.patternDesc)
		}
	}
}

// findJobsPackageRoot locates the absolute path of
// internal/application/jobs by walking up from cwd until it finds a
// go.mod with the expected jobs/ subtree.
//
// Hermetic — runs the same on every host (no absolute paths hardcoded).
func findJobsPackageRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			candidate := filepath.Join(dir, "internal", "application", "jobs")
			if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
				return candidate, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", os.ErrNotExist
}

// isGoCommentLine reports whether line is a Go comment line. Handles
// the common cases:
//   - Lines starting with `//` (after trimming leading whitespace)
//   - Lines starting with `/*` (block comment opener) or ending with `*/`
//   - Lines that are empty (no pattern to match anyway)
//
// The check is intentionally conservative: it ONLY excludes lines
// that are pure comments. Lines with code + trailing comment (e.g.
// `f, err := os.Open(path) // fix later`) still match — that's the
// correct behavior because the code call is real.
func isGoCommentLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	return strings.HasPrefix(trimmed, "//") ||
		strings.HasPrefix(trimmed, "/*") ||
		strings.HasPrefix(trimmed, "*")
}
