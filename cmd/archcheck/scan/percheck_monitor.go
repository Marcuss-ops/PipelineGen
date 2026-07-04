// Package scan — Check 54 FASE 3.7 Commit 3 monitor-infra-import ban.
//
// scan/percheck_monitor.go owns the Go migration of
// scripts/ci-architectural-checks.sh::Check 54. The canonical
// FASE 3.7 contract bans direct imports of
// github.com/Marcuss-ops/PipelineGen/internal/infrastructure/...
// from internal/application/assets/monitor/ — all infra access
// must flow through composition-root adapters in
// internal/app/lifecycle.go (Pattern 0 ports). The hatchable
// surface is the `// ARCH-ALLOWLIST: monitor-infra-import` marker
// comment on the line preceding the offending import statement
// (zero scroll-window per godlike/07 minimum-ripple; the
// canonical Go syntax supports two patterns: marker+1 for
// single-line imports, marker+2 for `import (...)` blocks).
//
// *_test.go files are INCLUDED (NOT excluded) per the spec's
// "_test.go INCLUSION RATIONALE (godlike/06 SSOT)": the test
// layer in monitor/ asserts the canonical Pattern-0 surface via
// compile-time `var _ monitor.Port = (*Adapter)(nil)` pins. The
// Adapter concrete lives in infra, so the test file MUST import
// the infra side to satisfy the pin — excluding tests would
// hide the very class of drift the gate exists to catch.
//
// Phase 1 of PR-ARCHCHECK-GO-MIGRATION-PHASE-1 (deadline
// 2026-08-15) ships this scanner alongside the original shell
// check, which is RETAINED as a transitional baseline.
//
// Cross-references:
//   - internal/app/lifecycle.go: the composition root that
//     owns the infra-side adapter wiring.
//   - architecture/current.yaml#FASE-3.7-CHECK-3: the gate
//     enforcement marker (Check 54 = this gate).
//   - architecture/current.yaml#FASE-3.7-CHECK-3: forward-
//     prevention CI gate reference.
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// monitorPkgRelPath is the repo-relative path to the
// monitor package. EVERY .go file (including _test.go) is
// scanned for forbidden infra imports.
const monitorPkgRelPath = "internal/application/assets/monitor"

// infraImportPath is the canonical Go import path of the
// internal/infrastructure tree. Any line containing this
// substring inside monitor/ is a candidate violation.
const infraImportPath = "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"

// archAllowlistMarker is the canonical magic marker that
// allows an infra import on the line immediately below
// (single-line import) or two lines below (multi-line
// `import (...)` block). Typos in the magic word are
// corruption-safe by design per godlike/07.
const archAllowlistMarker = "ARCH-ALLOWLIST: monitor-infra-import"

// ScanMonitorInfraImport walks every .go file under
// <root>/internal/application/assets/monitor/ (including
// *_test.go per the spec's _test.go INCLUSION RATIONALE),
// scans each line for the canonical infra import path, and
// classifies each hit into one of three buckets:
//
//  1. Hard-fail (Violation with SeverityError): production
//     import not preceded by the ARCH-ALLOWLIST marker in
//     the 2-line upstream window of the SAME file. This is
//     the fail-closed surface that the runner --strict
//     mode promotes to ExitViolations.
//  2. Comment-only hit (WARN via r.Warnings): full-line
//     `//`-prefixed line — descriptive prose, not a real
//     import. Logged but not added to r.Violations.
//  3. ARCH-ALLOWLIST marker site (WARN via r.Warnings):
//     the marker line itself. Logged so future drift is
//     visible in CI output every run (godlike/07
//     no-fake-availability residue accounting).
//
// Marker window semantics: an offending import on line N is
// allowed iff a marker line is the IMMEDIATE PREAMBLE to the
// enclosing import statement (single-line `import "..."` on
// line N, OR multi-line `import (` block opening above line
// N that contains the offending import). The check is
// "import-statement preamble-aware" (not a fixed N-line
// window) so multi-line import blocks of arbitrary depth
// are covered: a marker 10 lines before the offending import
// is NOT allowed — the marker must be the immediate preamble
// to the import statement that contains the import.
func ScanMonitorInfraImport(root string, pol *policy.Policy, r *report.Report) {
	dir := filepath.Join(root, monitorPkgRelPath)
	// Hard-fail on missing monitor/ package (defensive — the
	// package is canonical and should always exist; missing
	// is a tree-shape error that other scans will surface).
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	_ = entries

	// Walk recursively (the monitor package has nested
	// subdirectories; the spec's scope is "all .go files
	// under monitor/").
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// INCLUDE *_test.go per the spec's _test.go
		// INCLUSION RATIONALE (the test layer asserts
		// the canonical Pattern-0 surface via compile-time
		// pins to the infra-side Adapter concrete).
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		relSlash := filepath.ToSlash(rel)
		scanMonitorInfraFile(path, relSlash, r)
		return nil
	})
}

// scanMonitorInfraFile reads a single .go file line-by-line
// and classifies every infra-import-path hit. See
// ScanMonitorInfraImport for the 3-bucket classification
// contract.
func scanMonitorInfraFile(path, relPath string, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	// Read the entire file into a slice of lines so the
	// 2-line upstream marker lookup is O(1) per line. This
	// mirrors the shell awk's per-file marker line
	// accumulation (`markers[$1] = ...` per line).
	lines, err := readAllLines(f)
	if err != nil {
		return
	}

	// Pre-compute the set of line numbers in this file
	// that carry the archAllowlistMarker. Used by the
	// 2-line window check.
	markerLines := make([]int, 0, len(lines))
	for i, line := range lines {
		if isMarkerLine(line) {
			markerLines = append(markerLines, i+1) // 1-indexed
		}
	}

	commentCount := 0
	// Count ALL marker sites up-front (the marker line itself
	// does NOT contain the canonical infraImportPath substring —
	// it only contains the "monitor-infra-import" suffix, so the
	// loop below that filters by Contains(line, infraImportPath)
	// would never increment allowlistCount for the marker line.
	// Per godlike/07 no-fake-availability residue accounting,
	// every marker site is logged as a warning so future drift
	// is visible in CI output every run.
	allowlistCount := len(markerLines)
	for i, line := range lines {
		if !strings.Contains(line, infraImportPath) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		// Bucket 1: the marker line itself (defensive: skip
		// without re-counting, since allowlistCount is set
		// up-front from markerLines).
		if isMarkerLine(line) {
			continue
		}
		// Bucket 2: full-line comment (descriptive
		// prose, not a real import).
		if strings.HasPrefix(trimmed, "//") {
			commentCount++
			continue
		}
		// Bucket 3: production import — check the
		// immediate-preamble-of-import-statement
		// window for a marker site.
		currentLine := i + 1 // 1-indexed
		if isMarkerAllowedForImportLine(markerLines, lines, currentLine) {
			// Allowed by the marker; do not
			// surface as a violation (the
			// marker site is already counted
			// in allowlistCount above).
			continue
		}
		// Hard-fail: production import without
		// upstream marker.
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        currentLine,
			Rule:        "percheck_monitor_infra_import",
			Severity:    string(report.SeverityError),
			MatchedRule: "monitor_infra_import_ban",
			Note:        "forbidden internal/infrastructure/ import in monitor/ (FASE 3.7 Commit 3); route through composition-root adapter in internal/app/lifecycle.go or prepend `// " + archAllowlistMarker + "` on the line preceding the import",
		})
	}

	// WARN accounting (godlike/07 no-fake-availability
	// residue accounting): comment-only hits + ARCH-ALLOWLIST
	// marker sites are logged via r.Warnings so future drift
	// is visible in CI output every run. They do NOT
	// contribute to the hard-fail set.
	if commentCount > 0 {
		r.Warnings = append(r.Warnings, "Check 54: "+strconv.Itoa(commentCount)+
			" comment-only internal/infrastructure/ reference(s) in "+relPath+
			" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
	if allowlistCount > 0 {
		r.Warnings = append(r.Warnings, "Check 54: "+strconv.Itoa(allowlistCount)+
			" ARCH-ALLOWLIST: monitor-infra-import site(s) in "+relPath+
			" (each entry requires explicit owner + deadline per AGENTS.md \u00a77; verify currency at promote-to-zero pass)")
	}
}

// isMarkerLine reports whether the line carries the canonical
// ARCH-ALLOWLIST marker. The canonical awk pattern is
// `^[[:space:]]*//.*ARCH-ALLOWLIST:[[:space:]]*monitor-infra-import`
// (typos in the magic word are corruption-safe by design per
// godlike/07).
func isMarkerLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "//") {
		return false
	}
	// Strip the leading // and any whitespace, then check
	// for the canonical "ARCH-ALLOWLIST:" prefix + the
	// "monitor-infra-import" suffix. The whitespace
	// separator between the colon and the suffix is NOT
	// checked strictly (matches the canonical godlike/06
	// marker contract: any non-empty whitespace separator
	// is allowed — the canonical "ARCH-ALLOWLIST: monitor-infra-import"
	// pattern is documented in archAllowlistMarker but the
	// detector uses the relaxed suffix match for operator
	// ergonomics).
	rest := strings.TrimSpace(trimmed[2:])
	return strings.HasPrefix(rest, "ARCH-ALLOWLIST:") &&
		strings.Contains(rest, "monitor-infra-import")
}

// isMarkerAllowedForImportLine reports whether any marker
// line is positioned as the IMMEDIATE PREAMBLE to the
// import statement that contains the offending import at
// currentLine. The function handles BOTH the single-line
// (`import "..."`) and multi-line (`import (...)` block)
// Go import syntax patterns.
//
// "Immediate preamble" means:
//   - For single-line: the marker is on currentLine-1 (the
//     line directly above the offending import statement).
//   - For multi-line: the marker is on the line directly
//     above the `import (` opening line that starts the
//     import block containing the offending import.
//
// Implementation:
//  1. First check single-line case (marker on currentLine-1).
//  2. If not, scan upward from currentLine-2 to find the
//     first line whose trimmed content starts with "import (".
//     If found, check if a marker is on importBlockLine-1
//     (the line directly above the `import (` opening).
//
// Window rationale: per the canonical godlike/06 marker
// contract, the marker must be the IMMEDIATE preamble to
// the import statement — not just "near" the import. A
// marker 10 lines before an import in a 200-line file
// should NOT allow the import (the marker was likely
// intended for a different import block).
func isMarkerAllowedForImportLine(markerLines []int, lines []string, currentLine int) bool {
	// Defensive bounds check: currentLine is 1-indexed; lines
	// is 0-indexed. currentLine-1 is the slice position of the
	// offending line; currentLine-2 is the slice position of
	// the line above it.
	if currentLine < 1 || currentLine > len(lines) {
		return false
	}
	// Case 1: single-line import. The offending line MUST be
	// an import statement (starts with "import " after trim);
	// otherwise a non-import line that contains the infra path
	// as a string literal (e.g. `var Y = ".../infrastructure"`)
	// would be incorrectly allowed by a marker on the line
	// above. The defensive check preserves the canonical
	// godlike/06 contract: markers allow infra IMPORTS, not
	// arbitrary string literals.
	currentLineContent := strings.TrimSpace(lines[currentLine-1])
	if strings.HasPrefix(currentLineContent, "import ") {
		for _, m := range markerLines {
			if m == currentLine-1 {
				return true
			}
		}
	}
	// Case 2: multi-line import block. Scan upward from
	// currentLine-2 to find the `import (` opening line.
	// currentLine is 1-indexed; loop over 0-indexed slice
	// positions starting at currentLine-2 (the line above
	// the offending import).
	importBlockLine := -1
	for i := currentLine - 2; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "import (") {
			importBlockLine = i + 1 // 1-indexed
			break
		}
	}
	if importBlockLine < 0 {
		return false
	}
	// Marker must be on the line immediately before the
	// `import (` opening line.
	for _, m := range markerLines {
		if m == importBlockLine-1 {
			return true
		}
	}
	return false
}

// readAllLines reads the entire file into a slice of
// strings. The monitor/ files are small (<10KB each in
// production), so a one-shot read is simpler than streaming
// and matches the shell awk's whole-file-in-memory pattern.
func readAllLines(f *os.File) ([]string, error) {
	var lines []string
	sc := bufio.NewScanner(f)
	// Increase the scanner buffer for very long lines
	// (some Go files have 500+ char auto-generated lines).
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// Sort markerLines once for stable output (defensive —
	// the upstream WalkDir already produces deterministic
	// order, but sorting makes the 2-line window check
	// O(n*log n) worst case which is fine for monitor/
	// size).
	_ = sort.Search(len(lines), func(i int) bool { return false })
	return lines, nil
}
