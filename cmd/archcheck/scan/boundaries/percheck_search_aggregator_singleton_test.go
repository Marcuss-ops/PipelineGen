// Package scan — test for ScanSearchAggregatorSingleton
// (PR-DIAGNOSI-FINALE rule 6).
//
// Hermetic (t.TempDir-anchored). Validates the singleton
// invariant:
//
//  1. ZERO construction sites → SSOT-missing violation
//     (godlike/07 NO-FAKE-AVAILABILITY — phantom search
//     availability).
//  2. ONE construction site (canonical composition root) →
//     ZERO violations (the SSOT singleton assertion holds).
//  3. TWO construction sites → godlike/06 SSOT violation
//     (singleton-divergence — divergent search-routing
//     decisions).
//  4. Test files are exempt (regression-guard surface).
//  5. The regex requires the `search.` package qualifier
//     AND a function-call shape (parens immediately after) so
//     neighbouring names like NewAggregatorBuilder or
//     triggers.NewAggregator will NOT trip.
//  6. Comment-only references are residue-accounted.
package boundaries

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func makeFileForSearchAggregatorTest(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestScanSearchAggregatorSingleton_OneCanonicalSite verifies
// the canonical composition root emitting exactly one
// `search.NewAggregator(` produces ZERO violations.
func TestScanSearchAggregatorSingleton_OneCanonicalSite(t *testing.T) {
	root := t.TempDir()
	makeFileForSearchAggregatorTest(t, root, "internal/app/registry_search.go",
		`package app
import "search"
func Build() {
	_ = search.NewAggregator(nil, nil)
}
`)
	rep := &report.Report{}
	ScanSearchAggregatorSingleton(root, nil, rep)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("one canonical site tripped gate: got %d\nfirst: %s", got, rep.Violations[0].Note)
	}
}

// TestScanSearchAggregatorSingleton_ZeroSitesMissingSSOT
// verifies that ZERO construction sites emit a single
// SSOT-missing violation.
func TestScanSearchAggregatorSingleton_ZeroSitesMissingSSOT(t *testing.T) {
	root := t.TempDir()
	makeFileForSearchAggregatorTest(t, root, "internal/app/registry_search.go",
		`package app
func Build() {}
`)
	rep := &report.Report{}
	ScanSearchAggregatorSingleton(root, nil, rep)
	if got := len(rep.Violations); got != 1 {
		t.Fatalf("zero sites: expected exactly 1 SSOT-missing violation, got %d", got)
	}
	if rep.Violations[0].MatchedRule != "search_aggregator_singleton_missing" {
		t.Fatalf("missing matched rule = %q, want %q",
			rep.Violations[0].MatchedRule, "search_aggregator_singleton_missing")
	}
}

// TestScanSearchAggregatorSingleton_TwoSitesDuplicateSSOT
// verifies that 2 construction sites emit a single
// SSOT-divergence violation.
func TestScanSearchAggregatorSingleton_TwoSitesDuplicateSSOT(t *testing.T) {
	root := t.TempDir()
	makeFileForSearchAggregatorTest(t, root, "internal/app/registry_search.go",
		`package app
import "search"
func Build() {
	_ = search.NewAggregator(nil, nil)
}
`)
	makeFileForSearchAggregatorTest(t, root, "internal/app/bad_second.go",
		`package app
import "search"
func Build2() {
	_ = search.NewAggregator(nil, nil)
}
`)
	rep := &report.Report{}
	ScanSearchAggregatorSingleton(root, nil, rep)
	if got := len(rep.Violations); got != 1 {
		t.Fatalf("two sites: expected exactly 1 SSOT-divergence violation, got %d", got)
	}
	if rep.Violations[0].MatchedRule != "search_aggregator_singleton_duplicated" {
		t.Fatalf("divergence matched rule = %q, want %q",
			rep.Violations[0].MatchedRule, "search_aggregator_singleton_duplicated")
	}
}

// TestScanSearchAggregatorSingleton_TestFilesExempt verifies
// test files constructing via `NewAggregator(` (without
// `search.` prefix) are exempt.
func TestScanSearchAggregatorSingleton_TestFilesExempt(t *testing.T) {
	root := t.TempDir()
	makeFileForSearchAggregatorTest(t, root, "internal/application/search/aggregator_test.go",
		`package search
func TestBuildAgg() {
	_ = NewAggregator(nil, nil)
}
`)
	// NO canonical composition root — but the test file should
	// NOT be counted, so the singleton stays missing
	// (1 violation, the SSOT-missing one).
	rep := &report.Report{}
	ScanSearchAggregatorSingleton(root, nil, rep)
	if got := len(rep.Violations); got != 1 {
		t.Fatalf("test file counted as production: got %d violations, want 1 (SSOT-missing only)", got)
	}
	if rep.Violations[0].MatchedRule != "search_aggregator_singleton_missing" {
		t.Fatalf("matched rule = %q, want %q",
			rep.Violations[0].MatchedRule, "search_aggregator_singleton_missing")
	}
}

// TestScanSearchAggregatorSingleton_NeighbouringNamesDontTrip
// verifies the regex requires the `search.` qualifier and
// the function-call shape (parens immediately after).
func TestScanSearchAggregatorSingleton_NeighbouringNamesDontTrip(t *testing.T) {
	root := t.TempDir()
	makeFileForSearchAggregatorTest(t, root, "internal/app/registry_search.go",
		`package app
import "fmt"
func Build() {
	fmt.Println("search.NewAggregatorBuilder should not trip")
	fmt.Println("triggers.NewAggregator should not trip")
}
`)
	rep := &report.Report{}
	ScanSearchAggregatorSingleton(root, nil, rep)
	if got := len(rep.Violations); got != 1 {
		t.Fatalf("neighbouring names counted as canonical: got %d violations, want 1 (SSOT-missing only)", got)
	}
	if rep.Violations[0].MatchedRule != "search_aggregator_singleton_missing" {
		t.Fatalf("matched rule = %q, want %q",
			rep.Violations[0].MatchedRule, "search_aggregator_singleton_missing")
	}
}

// TestScanSearchAggregatorSingleton_CommentOnlyResidue
// verifies a comment-only reference is residue-accounted
// (WARN, not violation).
func TestScanSearchAggregatorSingleton_CommentOnlyResidue(t *testing.T) {
	root := t.TempDir()
	makeFileForSearchAggregatorTest(t, root, "internal/app/registry_search.go",
		`package app
// search.NewAggregator( is the canonical construction surface.
func Build() {}
`)
	rep := &report.Report{}
	ScanSearchAggregatorSingleton(root, nil, rep)
	if got := len(rep.Violations); got != 1 {
		t.Fatalf("comment-only produced unexpected violation count: got %d, want 1 (SSOT-missing only)", got)
	}
	if !containsString(rep.Warnings, "search-aggregator-singleton-comments:") {
		t.Fatalf("comment-only did NOT produce WARN: %v", rep.Warnings)
	}
	_ = strings.Join // silence unused-import if any
}
