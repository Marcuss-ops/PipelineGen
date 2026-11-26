// Package scan — pins the restored deprecation-registry validator.
package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func writeDeprecationFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func deprecationRecordYAML(id, removalDate, status string) string {
	return `deprecations:
    - id: ` + id + `
      owner_capability: internal/platform/example
      exact_symbol: ExampleSymbol
      file: internal/platform/example/file.go
      replacement: canonical surface
      introduction_date: 2026-01
      removal_date: ` + removalDate + `
      tracking_issue: TEST-1
      compatibility_test: go test ./...
      usage_metric: rg ExampleSymbol
      status: ` + status + `
      migration_phase: EXPAND
`
}

func deprecationIndexYAML(total int, bucket string, count int) string {
	return `total_records: ` + itoa(total) + `
records:
    - bucket: ` + bucket + `
      file: records/` + bucket + `.yaml
      count: ` + itoa(count) + `
`
}

func deprecationAuditYAML(total, inProgress, keep, other int) string {
	return `audit:
    total_records: ` + itoa(total) + `
    by_status:
        removed: 0
        in_progress: ` + itoa(inProgress) + `
        keep: ` + itoa(keep) + `
        other: ` + itoa(other) + `
    by_migration_phase:
        BACKFILL: 0
        CONTRACT: 0
        CUTOVER: 0
        EXPAND: ` + itoa(total) + `
        not applicable: 0
`
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

func TestScanDeprecationRegistry_ConsistentRegistryPasses(t *testing.T) {
	root := t.TempDir()
	writeDeprecationFile(t, root, "architecture/deprecations/records/alpha.yaml",
		deprecationRecordYAML("EXAMPLE-A", "2999-01", "in_progress"))
	writeDeprecationFile(t, root, "architecture/deprecations/index.yaml",
		deprecationIndexYAML(1, "alpha", 1))
	writeDeprecationFile(t, root, "architecture/deprecations/audit.yaml",
		deprecationAuditYAML(1, 1, 0, 0))

	r := &report.Report{}
	ScanDeprecationRegistry(root, &policy.Policy{}, r)

	if len(r.Violations) != 0 {
		t.Fatalf("expected no violations, got %+v", r.Violations)
	}
}

func TestScanDeprecationRegistry_DuplicateIDViolates(t *testing.T) {
	root := t.TempDir()
	writeDeprecationFile(t, root, "architecture/deprecations/records/alpha.yaml",
		deprecationRecordYAML("EXAMPLE-A", "2999-01", "in_progress"))
	writeDeprecationFile(t, root, "architecture/deprecations/records/beta.yaml",
		deprecationRecordYAML("EXAMPLE-A", "2999-01", "in_progress"))
	writeDeprecationFile(t, root, "architecture/deprecations/index.yaml",
		"total_records: 2\nrecords:\n    - bucket: alpha\n      file: records/alpha.yaml\n      count: 1\n    - bucket: beta\n      file: records/beta.yaml\n      count: 1\n")
	writeDeprecationFile(t, root, "architecture/deprecations/audit.yaml",
		deprecationAuditYAML(2, 2, 0, 0))

	r := &report.Report{}
	ScanDeprecationRegistry(root, &policy.Policy{}, r)

	if !strings.Contains(violationNotes(r), "duplicate id") {
		t.Fatalf("expected duplicate-id violation, got %+v", r.Violations)
	}
}

func TestScanDeprecationRegistry_MissingRequiredKeyViolates(t *testing.T) {
	root := t.TempDir()
	record := strings.Replace(
		deprecationRecordYAML("EXAMPLE-A", "2999-01", "in_progress"),
		"      replacement: canonical surface\n", "", 1)
	writeDeprecationFile(t, root, "architecture/deprecations/records/alpha.yaml", record)
	writeDeprecationFile(t, root, "architecture/deprecations/index.yaml",
		deprecationIndexYAML(1, "alpha", 1))
	writeDeprecationFile(t, root, "architecture/deprecations/audit.yaml",
		deprecationAuditYAML(1, 1, 0, 0))

	r := &report.Report{}
	ScanDeprecationRegistry(root, &policy.Policy{}, r)

	if !strings.Contains(violationNotes(r), `missing required key "replacement"`) {
		t.Fatalf("expected missing-key violation, got %+v", r.Violations)
	}
}

func TestScanDeprecationRegistry_OverdueIsWarningNotViolation(t *testing.T) {
	root := t.TempDir()
	writeDeprecationFile(t, root, "architecture/deprecations/records/alpha.yaml",
		deprecationRecordYAML("EXAMPLE-A", "2000-01", "in_progress"))
	writeDeprecationFile(t, root, "architecture/deprecations/index.yaml",
		deprecationIndexYAML(1, "alpha", 1))
	writeDeprecationFile(t, root, "architecture/deprecations/audit.yaml",
		deprecationAuditYAML(1, 1, 0, 0))

	r := &report.Report{}
	ScanDeprecationRegistry(root, &policy.Policy{}, r)

	if len(r.Violations) != 0 {
		t.Fatalf("overdue debt must not fail the gate, got %+v", r.Violations)
	}
	if !strings.Contains(strings.Join(r.Warnings, "\n"), "overdue") {
		t.Fatalf("expected an overdue warning, got %+v", r.Warnings)
	}
}

func TestScanDeprecationRegistry_IndexDriftViolates(t *testing.T) {
	root := t.TempDir()
	writeDeprecationFile(t, root, "architecture/deprecations/records/alpha.yaml",
		deprecationRecordYAML("EXAMPLE-A", "2999-01", "in_progress"))
	writeDeprecationFile(t, root, "architecture/deprecations/index.yaml",
		deprecationIndexYAML(9, "alpha", 9))
	writeDeprecationFile(t, root, "architecture/deprecations/audit.yaml",
		deprecationAuditYAML(9, 9, 0, 0))

	r := &report.Report{}
	ScanDeprecationRegistry(root, &policy.Policy{}, r)

	if !strings.Contains(violationNotes(r), "total_records is 9, records on disk are 1") {
		t.Fatalf("expected index drift violation, got %+v", r.Violations)
	}
}

func violationNotes(r *report.Report) string {
	notes := make([]string, 0, len(r.Violations))
	for _, violation := range r.Violations {
		notes = append(notes, violation.Note)
	}
	return strings.Join(notes, "\n")
}
