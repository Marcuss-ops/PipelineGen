package boundaries

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func TestScanCanonicalApplicationInfrastructureImports(t *testing.T) {
	t.Run("clean production area passes and ignores comments/tests", func(t *testing.T) {
		root := t.TempDir()
		writeCanonicalFixture(t, root, "internal/capabilities/images/workflow/clean.go", `package images

// import "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
const example = "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
`)
		writeCanonicalFixture(t, root, "internal/capabilities/images/workflow/clean_test.go", `package images
import "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
`)

		r := &report.Report{}
		ScanCanonicalApplicationInfrastructureImports(root, &policy.Policy{
			CanonicalApplicationAreas: []string{"internal/capabilities/images/workflow"},
		}, r)
		if len(r.Violations) != 0 {
			t.Fatalf("clean area produced violations: %#v", r.Violations)
		}
	})

	t.Run("production infrastructure import fails", func(t *testing.T) {
		root := t.TempDir()
		writeCanonicalFixture(t, root, "internal/capabilities/images/workflow/bad.go", `package images

import storage "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"

var _ storage.Reader
`)

		r := &report.Report{}
		ScanCanonicalApplicationInfrastructureImports(root, &policy.Policy{
			CanonicalApplicationAreas: []string{"internal/capabilities/images/workflow"},
		}, r)
		hits := violationsWithRule(r.Violations, canonicalApplicationInfraImportRule)
		if len(hits) != 1 {
			t.Fatalf("want one import violation, got %#v", r.Violations)
		}
		if hits[0].Line != 3 || hits[0].Severity != string(report.SeverityError) {
			t.Fatalf("unexpected violation: %#v", hits[0])
		}
	})

	t.Run("missing area fails closed", func(t *testing.T) {
		root := t.TempDir()
		r := &report.Report{}
		ScanCanonicalApplicationInfrastructureImports(root, &policy.Policy{
			CanonicalApplicationAreas: []string{"internal/capabilities/images/workflow"},
		}, r)
		hits := violationsWithRule(r.Violations, canonicalApplicationMissingAreaRule)
		if len(hits) != 1 || hits[0].Severity != string(report.SeverityError) {
			t.Fatalf("want one error for missing area, got %#v", r.Violations)
		}
	})

	t.Run("enabled gate requires declared areas", func(t *testing.T) {
		root := t.TempDir()
		r := &report.Report{}
		ScanCanonicalApplicationInfrastructureImports(root, &policy.Policy{
			HardGates: []string{canonicalApplicationInfraImportRule},
		}, r)
		hits := violationsWithRule(r.Violations, canonicalApplicationMissingAreaRule)
		if len(hits) != 1 || hits[0].Severity != string(report.SeverityError) {
			t.Fatalf("want one fail-closed missing-area violation, got %#v", r.Violations)
		}
	})

	t.Run("malformed production file fails closed", func(t *testing.T) {
		root := t.TempDir()
		writeCanonicalFixture(t, root, "internal/capabilities/images/workflow/bad.go", "package images\nimport (\n")
		r := &report.Report{}
		ScanCanonicalApplicationInfrastructureImports(root, &policy.Policy{
			CanonicalApplicationAreas: []string{"internal/capabilities/images/workflow"},
		}, r)
		hits := violationsWithRule(r.Violations, canonicalApplicationParseErrorRule)
		if len(hits) != 1 || !strings.Contains(hits[0].Note, "fail-closed") {
			t.Fatalf("want one fail-closed parse error, got %#v", r.Violations)
		}
	})
}

func writeCanonicalFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func violationsWithRule(violations []report.Violation, rule string) []report.Violation {
	var out []report.Violation
	for _, violation := range violations {
		if violation.Rule == rule {
			out = append(out, violation)
		}
	}
	return out
}
