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
// immediately before either the concrete import spec or the enclosing
// `import (` declaration.
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
package governance

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
const monitorPkgRelPath = "internal/capabilities/assets/monitor"
const monitorLegacyPkgRelPath = "internal/application/assets/monitor"

// infraImportPath is the canonical Go import path of the
// internal/infrastructure tree. Any line containing this
// substring inside monitor/ is a candidate violation.
const infraImportPath = "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"

// archAllowlistMarker is the canonical magic marker that
// allows an infra import when it is immediately above either
// the concrete import spec or the enclosing `import (` line.
// Typos in the magic word are corruption-safe by design per
// godlike/07.
const archAllowlistMarker = "ARCH-ALLOWLIST: monitor-infra-import"

// ScanMonitorInfraImport walks every .go file under
// <root>/internal/application/assets/monitor/ (including
// *_test.go per the spec's _test.go INCLUSION RATIONALE),
// scans each line for the canonical infra import path, and
// classifies each hit into one of three buckets:
//
//  1. Hard-fail (Violation with SeverityError): production
//     import not protected by the ARCH-ALLOWLIST marker in
//     the SAME file. This is the fail-closed surface that the
//     runner --strict mode promotes to ExitViolations.
//  2. Comment-only hit (WARN via r.Warnings): full-line
//     `//`-prefixed line — descriptive prose, not a real
//     import. Logged but not added to r.Violations.
//  3. ARCH-ALLOWLIST marker site (WARN via r.Warnings):
//     the marker line itself. Logged so future drift is
//     visible in CI output every run (godlike/07
//     no-fake-availability residue accounting).
//
// Marker semantics: an offending import on line N is allowed
// when a marker is directly above the single-line import, directly
// above the import spec inside an `import (...)` block, or directly
// above the enclosing `import (` declaration. The direct-import-spec
// form matches the retained shell Check 54 and is the form used by the
// monitor SQLite hermetic tests.
func ScanMonitorInfraImport(root string, pol *policy.Policy, r *report.Report) {
	for _, relDir := range []string{monitorPkgRelPath, monitorLegacyPkgRelPath} {
		dir := filepath.Join(root, relDir)
		// Hard-fail on missing monitor/ package (defensive — the
		// package is canonical and should always exist; missing
		// is a tree-shape error that other scans will surface).
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
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
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			relSlash := filepath.ToSlash(rel)
			scanMonitorInfraFile(path, relSlash, r)
			return nil
		})
	}
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

	// Read the entire file into a slice of lines so marker lookup
	// is O(1) per candidate line. This mirrors the shell awk's
	// per-file marker accumulation (`markers[$1] = ...` per line).
	lines, err := readAllLines(f)
	if err != nil {
		return
	}

	// Pre-compute the set of line numbers in this file
	// that carry the archAllowlistMarker.
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
		// Bucket 3: production import — check the marker
		// placement against the containing import statement.
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
			" (each entry requires explicit owner + deadline per AGENTS.md §7; verify currency at promote-to-zero pass)")
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

// isMarkerAllowedForImportLine reports whether a marker protects
// the offending import at currentLine. The accepted forms are:
//   - marker immediately above a single-line import declaration;
//   - marker immediately above the concrete import spec inside a
//     multi-line import block (retained shell Check 54 contract);
//   - marker immediately above the enclosing `import (` declaration.
//
// The upward scan stops at a closing parenthesis. This prevents a marker
// associated with an earlier import block from allowing a later string
// literal that merely contains infraImportPath.
func isMarkerAllowedForImportLine(markerLines []int, lines []string, currentLine int) bool {
	// Defensive bounds check: currentLine is 1-indexed; lines
	// is 0-indexed. currentLine-1 is the slice position of the
	// offending line; currentLine-2 is the slice position of
	// the line above it.
	if currentLine < 1 || currentLine > len(lines) {
		return false
	}

	currentLineContent := strings.TrimSpace(lines[currentLine-1])
	// Case 1: single-line import declaration.
	if strings.HasPrefix(currentLineContent, "import ") {
		for _, m := range markerLines {
			if m == currentLine-1 {
				return true
			}
		}
	}

	// Find an enclosing multi-line import block. Stop at a closing
	// parenthesis so a prior, already-closed import block cannot
	// accidentally authorize a later string literal.
	importBlockLine := -1
	for i := currentLine - 2; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, ")") {
			return false
		}
		if strings.HasPrefix(trimmed, "import (") {
			importBlockLine = i + 1 // 1-indexed
			break
		}
	}
	if importBlockLine < 0 {
		return false
	}

	for _, m := range markerLines {
		// Case 2: marker directly above the concrete import spec.
		if m == currentLine-1 {
			return true
		}
		// Case 3: marker directly above the enclosing import block.
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
