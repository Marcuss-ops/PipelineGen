// Package scan — percheck_version_strings_ban_test.go pins the
// forward-prevention contract for the version-string literal ban.
package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func versionStringsTestReport() *report.Report {
	return &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
	}
}

func versionStringsWriteTree(t *testing.T, dir string, files map[string]string) {
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

func versionStringsViolations(r *report.Report) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == "percheck_version_strings_ban" {
			out = append(out, v)
		}
	}
	return out
}

// TestVersionStrings_HardcodedPipelineVersionFails verifies that a
// hardcoded component version outside the registry trips the gate.
func TestVersionStrings_HardcodedPipelineVersionFails(t *testing.T) {
	dir := t.TempDir()
	versionStringsWriteTree(t, dir, map[string]string{
		"internal/application/brain/core/core.go": `package core
const BrainVersion = "brain-v1"
`,
	})
	r := versionStringsTestReport()
	ScanVersionStringsBan(dir, &policy.Policy{}, r)
	viol := versionStringsViolations(r)
	if len(viol) != 1 {
		t.Fatalf("want 1 violation, got %d: %+v", len(viol), r.Violations)
	}
	if !strings.Contains(viol[0].Note, "brain-v1") {
		t.Fatalf("violation note must reference the offending version; got %q", viol[0].Note)
	}
}

// TestVersionStrings_RegistryOwnerPasses verifies that the canonical
// version registry may contain the literal version strings.
func TestVersionStrings_RegistryOwnerPasses(t *testing.T) {
	dir := t.TempDir()
	versionStringsWriteTree(t, dir, map[string]string{
		"internal/kernel/media/version.go": `package media
const VersionBrain = "brain-v1"
`,
	})
	r := versionStringsTestReport()
	ScanVersionStringsBan(dir, &policy.Policy{}, r)
	if got := len(versionStringsViolations(r)); got != 0 {
		t.Fatalf("want 0 violations inside version registry, got %d: %+v", got, r.Violations)
	}
}

// TestVersionStrings_NonPipelineVersionIgnored verifies that unrelated
// version strings such as schema model versions are not flagged.
func TestVersionStrings_NonPipelineVersionIgnored(t *testing.T) {
	dir := t.TempDir()
	versionStringsWriteTree(t, dir, map[string]string{
		"internal/platform/qdrant/schema/concept_schema.go": `package schema
const ModelVersion = "2026-06-16-v1"
`,
	})
	r := versionStringsTestReport()
	ScanVersionStringsBan(dir, &policy.Policy{}, r)
	if got := len(versionStringsViolations(r)); got != 0 {
		t.Fatalf("want 0 violations for non-pipeline version, got %d: %+v", got, r.Violations)
	}
}

// TestVersionStrings_TestFilesExempted verifies that test files are
// ignored.
func TestVersionStrings_TestFilesExempted(t *testing.T) {
	dir := t.TempDir()
	versionStringsWriteTree(t, dir, map[string]string{
		"internal/application/brain/core/core_test.go": `package core
const BrainVersion = "brain-v1"
`,
	})
	r := versionStringsTestReport()
	ScanVersionStringsBan(dir, &policy.Policy{}, r)
	if got := len(versionStringsViolations(r)); got != 0 {
		t.Fatalf("want 0 violations inside test files, got %d: %+v", got, r.Violations)
	}
}
