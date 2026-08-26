// Package main — archcheck checker registry + shared utilities.
//
// checks.go owns the SLIM Package-orchestrator shell + the 2 truly
// shared utilities used by every sister file in this package:
//
//   - execErrIsNoMatch: the canonical "rg returned no hits" classification
//     helper. All sister files (checks_imports.go / checks_coupling.go /
//     checks_patterns.go / phase0_checks.go) call it through package
//     visibility (same `main` package) — no additional import needed.
//
//   - splitNonEmpty: the canonical "split lines, drop empties, sort" string
//     utility. All sister files use it for consistent rg-output
//     normalization.
//
// All actual check body logic lives in 3 sibling files (godlike/06 SSOT
// one-canonical-owner-per-fact):
//
//   - checks_imports.go: canonical-root and cross-capability import rules.
//
//   - checks_coupling.go: package-dependency coupling rule via
//     checkDatabaseSQLGate.
//
//   - checks_patterns.go (~130 LOC): anti-pattern detection —
//     checkMigrationYAML + checkOwnershipYAML + checkPythonLegacyWriterGate
//
//   - their helpers/regexes/structs.
//
// Phase 0 baseline-on-baseline rules (the 5 separate rules that run via
// --future-ratchet and compare against scripts/archcheck/phase0_baseline.json)
// live in phase0_checks.go — different lifecycle from the Wave 14-18
// checks here (PR-A bootstrap, baseline.json diff, future-ratchet
// promotion), so they keep their own home per Wave 19 PR-A design.
package main

import (
	"os/exec"
	"sort"
	"strings"
)

// execErrIsNoMatch reports whether the given error is the canonical
// "ripgrep returned no matches" sentinel — exit code 1 with no stderr.
// All rg-driven check functions classify this case as "empty hit set"
// (vs the "-1 violation" sentinel that signals rg itself failed).
//
// Lives in checks.go (not in a sister file) because EVERY rg-driven
// check in this package uses the same classification: checkRetiredRootImports
// and checkCrossCapabilityImport in checks_imports.go, checkDatabaseSQLGate
// in checks_coupling.go,
// and the 4 rg-driven Phase 0 rules in phase0_checks.go
// (checkSetterDetector + checkTypeAliasCrossPkg + checkFakeRoute all
// need the same "exit 1 = no hits" semantic).
func execErrIsNoMatch(err error) bool {
	if err == nil {
		return false
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode() == 1
	}
	return false
}

// splitNonEmpty splits s on "\n", trims whitespace, drops empty lines,
// then returns a sorted slice. The canonical rg-output post-processor
// for ALL rg-driven checks: rg emits one match per line, "-ln" mode
// emits paths one per line, and downstream comparison (via
// bl.NormalizePaths + bl.SubtractSet) requires sorted determinism.
//
// Lives in checks.go (not in a sister file) because it is a string util
// shared by 4+ checks across the package:
//
//   - checks_patterns.go: checkPythonLegacyWriterGate is file-based
//     (NOT eated by splitNonEmpty) but historically inherited the
//     helper from this file's pre-split topology
//   - phase0_checks.go: checkSetterDetector +
//     checkTypeAliasCrossPkg + checkFakeRoute (rg -n line output)
//
// Sorting guarantees deterministic violation ordering across CI runs.
func splitNonEmpty(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}
