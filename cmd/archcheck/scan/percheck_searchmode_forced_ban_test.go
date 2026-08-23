// Package scan — percheck_searchmode_forced_ban_test.go pins the
// forward-prevention contract for the SearchModeANN forced ban.
package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func searchModeForcedTestReport() *report.Report {
	return &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
	}
}

func searchModeForcedWriteTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for relPath, contents := range files {
		fullPath := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(fullPath), err)
		}
		if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %q: %v", fullPath, err)
		}
	}
}

func searchModeForcedViolations(r *report.Report) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == "percheck_searchmode_forced_ban" {
			out = append(out, v)
		}
	}
	return out
}

// TestSearchModeForced_AdapterAssignmentFails verifies that a
// hardcoded `Mode: search.SearchModeANN` in an adapter trips the gate.
func TestSearchModeForced_AdapterAssignmentFails(t *testing.T) {
	dir := t.TempDir()
	searchModeForcedWriteTree(t, dir, map[string]string{
		"internal/application/mediamemory/adapters/search.go": `package adapters
import "github.com/Marcuss-ops/PipelineGen/internal/application/search"
func buildQuery() search.Query {
	return search.Query{Mode: search.SearchModeANN}
}
`,
	})
	r := searchModeForcedTestReport()
	ScanSearchModeForcedBan(dir, &policy.Policy{}, r)
	viol := searchModeForcedViolations(r)
	if len(viol) != 1 {
		t.Fatalf("want 1 violation, got %d: %+v", len(viol), r.Violations)
	}
	if !strings.Contains(viol[0].Note, "SearchModeANN") {
		t.Fatalf("violation note must reference SearchModeANN; got %q", viol[0].Note)
	}
}

// TestSearchModeForced_ExemptDomainOwner verifies that the domain
// policy SSOT is exempt.
func TestSearchModeForced_ExemptDomainOwner(t *testing.T) {
	dir := t.TempDir()
	searchModeForcedWriteTree(t, dir, map[string]string{
		"internal/kernel/media/search_policy.go": `package media
type SearchMode string
const SearchModeANN SearchMode = "ann"
`,
	})
	r := searchModeForcedTestReport()
	ScanSearchModeForcedBan(dir, &policy.Policy{}, r)
	if got := len(searchModeForcedViolations(r)); got != 0 {
		t.Fatalf("want 0 violations inside domain SSOT, got %d: %+v", got, r.Violations)
	}
}

// TestSearchModeForced_ExemptTestFiles verifies that test files are
// ignored.
func TestSearchModeForced_ExemptTestFiles(t *testing.T) {
	dir := t.TempDir()
	searchModeForcedWriteTree(t, dir, map[string]string{
		"internal/application/mediamemory/adapters/search_test.go": `package adapters
import "github.com/Marcuss-ops/PipelineGen/internal/application/search"
var q = search.Query{Mode: search.SearchModeANN}
`,
	})
	r := searchModeForcedTestReport()
	ScanSearchModeForcedBan(dir, &policy.Policy{}, r)
	if got := len(searchModeForcedViolations(r)); got != 0 {
		t.Fatalf("want 0 violations inside test files, got %d: %+v", got, r.Violations)
	}
}

// TestSearchModeForced_EqualityComparisonIgnored verifies that a
// comparison (`mode == search.SearchModeANN`) is not flagged.
func TestSearchModeForced_EqualityComparisonIgnored(t *testing.T) {
	dir := t.TempDir()
	searchModeForcedWriteTree(t, dir, map[string]string{
		"internal/application/mediamemory/resolver.go": `package mediamemory
import "github.com/Marcuss-ops/PipelineGen/internal/application/search"
func isANN(mode search.SearchMode) bool { return mode == search.SearchModeANN }
`,
	})
	r := searchModeForcedTestReport()
	ScanSearchModeForcedBan(dir, &policy.Policy{}, r)
	if got := len(searchModeForcedViolations(r)); got != 0 {
		t.Fatalf("want 0 violations for equality comparison, got %d: %+v", got, r.Violations)
	}
}
