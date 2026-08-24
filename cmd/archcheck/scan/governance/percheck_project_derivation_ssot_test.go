// Package scan — percheck_project_derivation_ssot_test.go pins the
// forward-prevention contract for the "scene" project-namespace fallback ban.
package governance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func projectDerivationTestReport() *report.Report {
	return &report.Report{Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}}}
}

func projectDerivationWriteTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, contents := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %q: %v", full, err)
		}
	}
}

func projectDerivationViolations(r *report.Report) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == projectDerivationSSOTRule {
			out = append(out, v)
		}
	}
	return out
}

func TestProjectDerivationSSOT_SceneFallbackViolates(t *testing.T) {
	dir := t.TempDir()
	projectDerivationWriteTree(t, dir, map[string]string{
		"internal/foo/routing.go": `package foo
func f() string { project := ""; if project == "" { project = "scene" }; return project }
`,
	})
	r := projectDerivationTestReport()
	ScanProjectDerivationSSOT(dir, &policy.Policy{}, r)
	if got := len(projectDerivationViolations(r)); got != 1 {
		t.Fatalf("want 1 violation, got %d: %+v", got, r.Violations)
	}
}

func TestProjectDerivationSSOT_VerbatimPropagationPasses(t *testing.T) {
	dir := t.TempDir()
	projectDerivationWriteTree(t, dir, map[string]string{
		"internal/kernel/script/routing_context.go": `package script
type ArtifactRoutingContext struct{ Project string }
`,
	})
	r := projectDerivationTestReport()
	ScanProjectDerivationSSOT(dir, &policy.Policy{}, r)
	if got := len(projectDerivationViolations(r)); got != 0 {
		t.Fatalf("want 0 violations for verbatim propagation, got %d: %+v", got, r.Violations)
	}
}

func TestProjectDerivationSSOT_TestFilesExempt(t *testing.T) {
	dir := t.TempDir()
	projectDerivationWriteTree(t, dir, map[string]string{
		"internal/foo/routing_test.go": `package foo
var _ = "scene"
`,
	})
	r := projectDerivationTestReport()
	ScanProjectDerivationSSOT(dir, &policy.Policy{}, r)
	if got := len(projectDerivationViolations(r)); got != 0 {
		t.Fatalf("want 0 violations in test files, got %d", got)
	}
}
