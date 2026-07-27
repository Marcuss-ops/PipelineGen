// Package scan — file-line-strict scanner.
//
// scan/file_lines_strict.go owns the "600-LOC per-file soft cap"
// forward-prevention gate (godlike/08 §"Mandatory checks" +
// godlike/07 ZERO_LEGACY_POLICY §"Default rule"). It walks every
// non-test .go file under internal/ and cmd/ and emits a
// warn-severity violation per file whose line count exceeds
// pol.MaxLinesPerFileStrict, unless the file's repo-relative
// forward-slashed path is listed in the allowlist pointed to by
// pol.MaxLinesStrictAllowlist.
//
// Relationship to the legacy max_lines_per_file=1000 rule
// (ScanPackagesForMode / ScanCommandBinaries):
//
//   - max_lines_per_file=1000 — absolute hard ceiling. Existing.
//   - max_lines_per_file_strict=600 — soft warning that fires BEFORE
//     the 1000 hard cap, giving operators a chance to split
//     proactively. Files in max_lines_strict_allowlist are exempt
//     (per godlike/07 §"Temporary deprecation record"); the allowlist
//     is the canonical escape hatch per godlike/08 §"Zero-baseline
//     rule" (each entry must carry owner + deadline +
//     removal_trigger).
//
// Allowlist format (plain text):
//
//   - one repo-root-relative forward-slashed path per line
//   - `#`-prefixed comments are ignored
//   - blank lines are ignored
//   - path normalization is case-sensitive; trim whitespace
//
// Skipped dirs (mirrors ScanConstructors / ScanStaleProsePaths):
// .git, vendor, node_modules, node-scraper, examples, scripts.
// _test.go files are skipped: they are test fixtures, not
// production code, and the rule family targets production
// cohesion. Tests-size governance is a separate concern (not in
// scope here). When pol.MaxLinesPerFileStrict <= 0 the function is
// a no-op (the policy opts out of the rule family).
package scan

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// ScanFileLinesStrict walks internal/ + cmd/ and emits one
// warn-severity violation per .go file exceeding
// pol.MaxLinesPerFileStrict lines, unless the file path is listed
// in pol.MaxLinesStrictAllowlist.
//
// The cap is a SOFT warning — promoted to report.SeverityError
// only via pol.HardGates (godlike/08 hard-gate promotion cadence).
// Hard caps remain owned by the legacy max_lines_per_file=1000
// rule family (cmd/archcheck/scan/packages.go::ScanPackagesForMode
// + ScanCommandBinaries). Adding "max_lines_per_file_strict" to
// pol.HardGates would promote this rule's failures to os.Exit(1)
// unconditionally, mirroring the same promotion for the legacy
// rule family per the existing runner.go hard-gate loop.
func ScanFileLinesStrict(root string, pol *policy.Policy, r *report.Report) {
	if pol.MaxLinesPerFileStrict <= 0 {
		return
	}
	allowlist := loadLineStrictAllowlist(root, pol.MaxLinesStrictAllowlist)
	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
	}
	for _, sub := range []string{"internal", "cmd"} {
		base := filepath.Join(root, sub)
		_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if skipDirs[filepath.Base(path)] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			lines, err := countFileLines(path)
			if err != nil {
				return nil
			}
			if lines <= pol.MaxLinesPerFileStrict {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			relPath := filepath.ToSlash(rel)
			if allowlist[relPath] {
				return nil
			}
			r.Violations = append(r.Violations, report.Violation{
				File:        relPath,
				ActualLines: lines,
				MaxLines:    pol.MaxLinesPerFileStrict,
				MatchedRule: "max_lines_per_file_strict",
				Rule:        "max_lines_per_file_strict",
				Severity:    "warn",
				Note: fmt.Sprintf(
					"file is %d lines (max %d); split proactively before crossing the 1000-LOC hard cap (godlike/08 forward-prevention gate; allowlist opt-out per godlike/07 §\"Temporary deprecation record\")",
					lines, pol.MaxLinesPerFileStrict,
				),
			})
			return nil
		})
	}
}

// countFileLines counts the lines of a single file by scanning it
// with bufio.Scanner. Scanner's default 64K line cap is bumped to
// 1M so a single long line (e.g. a generated 500K-char string) does
// not silently truncate the count. Returns 0 + the scanner's error
// on I/O failure.
func countFileLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return n, nil
}

// loadLineStrictAllowlist reads the plain-text allowlist pointed to
// by `path` (relative to root) and returns a set of forward-slashed
// paths that exempt their corresponding .go file from
// ScanFileLinesStrict. Empty lines + line-leading `#` comments are
// ignored. Inline trailing `# ...` annotations (preceded by
// whitespace, e.g. `path  # owner=@team, deadline=2026-XX-XX`) are
// stripped before keying so operators can attach per-entry
// governance metadata inline without silently breaking the
// exemption. Without this strip, the path-and-annotation concatenated
// string would not match the bare path produced by filepath.Rel +
// filepath.ToSlash, and the entry would fail-closed as a no-op
// (silently invisible to the scanner). Missing or empty path
// returns an empty set (the rule is then unenforced for the
// un-allowlisted population — equivalent to opt-out without
// setting MaxLinesPerFileStrict=0).
func loadLineStrictAllowlist(root, path string) map[string]bool {
	out := map[string]bool{}
	if path == "" {
		return out
	}
	f, err := os.Open(filepath.Join(root, path))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip inline trailing `# ...` annotation. The literal
		// " #" (space then hash) is the canonical separator used
		// across the project's existing allowlists (admin-sql-
		//  allowlist.txt, duplicates-types-allowlist.txt); paths
		// containing "#" without a preceding whitespace (e.g.
		// URL-fragment paths) are NOT considered annotations.
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
			if line == "" {
				continue
			}
		}
		out[filepath.ToSlash(line)] = true
	}
	return out
}
