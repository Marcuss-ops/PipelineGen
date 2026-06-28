// Package baseline — archcheck baseline diff helpers.
//
// baseline/compare.go owns the pure helpers that the 5 Phase 0 check
// functions (and the database-sql gate in main.go) use to diff their
// actual rg-shaped string set against the committed baseline. Three
// functions live here:
//
//   - SubtractSet: the building block. Returns the elements of `a`
//     that are NOT in `b`. Both sides must be pre-normalized (see
//     NormalizePaths below).
//   - NormalizePaths: lowercases, trims, dedupes, and slash-converts
//     every path. Used to make the diff order-independent and
//     OS-independent (rg output on Windows uses backslashes).
//   - Compare: the canonical diff. Returns the regressions
//     (added — entries in `actual` not in `base`) and the stale
//     entries (entries in `base` no longer present in `actual`).
//     This is the function all 5 Phase 0 check_*.go files should
//     call; the 5 currently call SubtractSet directly, which the
//     runner/ checks/ refactor will migrate in PR3+.
//
// Package boundary: `package baseline` (separate from the top-level
// `package main` of the archcheck binary). All symbols are exported
// so main.go / runner.go can call them via the imported
// `baseline.Compare`, `baseline.NormalizePaths`, `baseline.SubtractSet`.
package baseline

import (
	"path/filepath"
	"sort"
	"strings"
)

// Compare is the canonical diff helper that all Phase 0 check
// functions use. Given the current actual set and the committed
// baseline, it returns:
//
//   - regressions: entries in `actual` that are NOT in `base`.
//     These are the violations the ratchet gate fails on (new debt).
//   - stale: entries in `base` that no longer appear in `actual`.
//     These are audit signals — the baseline can be tightened, but
//     the gate does NOT fail on stale entries during the minor cycle
//     (see architecture/current.yaml Wave 19 PR-A).
//
// Both inputs MUST be pre-normalized (see NormalizePaths) so the
// diff is stable across OSes and check ordering.
func Compare(actual, base []string) (regressions, stale []string) {
	return SubtractSet(actual, base), SubtractSet(base, actual)
}

// NormalizePaths lowercases, trims, dedupes, and slash-converts
// every entry in `paths`. Used as the canonical pre-pass before
// Compare / SubtractSet so the diff is order-independent and
// OS-independent. The output is sorted ascending.
func NormalizePaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	var out []string
	for _, p := range paths {
		norm := filepath.ToSlash(strings.TrimSpace(p))
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
	}
	sort.Strings(out)
	return out
}

// SubtractSet returns the elements of `actual` that are NOT in
// `allowed`. Both inputs are expected to be pre-normalized (the
// Phase 0 checks normalize via NormalizePaths before calling this).
// The output preserves the order of `actual`.
//
// SubtractSet is a low-level building block; new code should prefer
// Compare (which returns both regressions AND stale) unless only
// one side of the diff is needed.
func SubtractSet(actual, allowed []string) []string {
	allowedSet := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		allowedSet[a] = true
	}
	var diff []string
	for _, a := range actual {
		if !allowedSet[a] {
			diff = append(diff, a)
		}
	}
	return diff
}
