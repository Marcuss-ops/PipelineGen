// Package scan — Wave-22 percheck_api_infrastructure_imports tests.
//
// scan/percheck_api_infrastructure_imports_test.go owns the unit
// coverage for the Wave-22 forward-prevention gate. 5 isolated
// t.TempDir cases cover the happy path, the sentinel infra-import
// hit, the allowlist bypass, the fail-closed missing-allowlist
// path, and the zero-baseline stale-entry path.
//
// The cases mirror what scripts/ci/architecture/checks/check_19
// validates at the shell level; the Go-side test gives the
// Wave-22 hard-gate promotion its own on-default coverage line.
package boundaries

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// TestPercheckAPIInfrastructureImports exercises the Wave-22
// forward-prevention gate. Each subtest builds a t.TempDir() with
// a controlled file layout so named cases stay byte-stable
// (no dependency on the live repo state).
func TestPercheckAPIInfrastructureImports(t *testing.T) {
	t.Run("clean_repo_no_hit", func(t *testing.T) {
		root := t.TempDir()
		writeFixtureFile(t, filepath.Join(root, "internal", "api", "handler.go"),
			"package api\n\nimport \"net/http\"\n\nfunc X() *http.Request { return nil }\n")
		writeFixtureFile(t, filepath.Join(root, apiInfraImportAllowlistFile), "")

		r := &report.Report{}
		ScanAPIInfrastructureImports(root, &policy.Policy{}, r)

		if got := len(r.Violations); got != 0 {
			t.Fatalf("clean_repo_no_hit: want 0 violations, got %d (%v)", got, summarizeViolations(r.Violations))
		}
	})

	t.Run("sentinel_infra_import_violation", func(t *testing.T) {
		root := t.TempDir()
		writeFixtureFile(t, filepath.Join(root, "internal", "api", "handler.go"),
			"package api\n\nimport \"net/http\"\nimport drv \""+apiInfraImportBanned+"drive\"\n\nfunc X(http.Handler, drv.Admin) {}\n")
		writeFixtureFile(t, filepath.Join(root, apiInfraImportAllowlistFile), "")

		r := &report.Report{}
		ScanAPIInfrastructureImports(root, &policy.Policy{}, r)

		// Expect: 1 violation from the infra import (dr.Admin) +
		// (depending on file content) a fail-closed if the
		// allowlist row above is empty AND the file is empty.
		// Empty file = empty allowlist = no fail-closed; this
		// test confirms the empty allowlist parses cleanly.
		hits := filterByRule(r.Violations, "percheck_api_infrastructure_imports")
		if len(hits) != 1 {
			t.Fatalf("sentinel_infra_import_violation: want exactly 1 infra-import hit, got %d (%v)", len(hits), summarizeViolations(r.Violations))
		}
		if got := hits[0].File; got != "internal/api/handler.go" {
			t.Fatalf("File = %q, want internal/api/handler.go", got)
		}
		if hits[0].Severity != "warn" {
			t.Fatalf("Severity = %q, want warn", hits[0].Severity)
		}
	})

	t.Run("allowlisted_import_no_violation", func(t *testing.T) {
		root := t.TempDir()
		writeFixtureFile(t, filepath.Join(root, "internal", "api", "handler.go"),
			"package api\n\nimport drv \""+apiInfraImportBanned+"drive\"\n\nvar _ drv.Admin\n")

		// With a single canonical allowlist entry naming the
		// file, the percheck must accept that as the documented
		// grandfathered surface (godlike/06 SSOT-marker), not
		// emit a violation.
		writeFixtureFile(t, filepath.Join(root, apiInfraImportAllowlistFile),
			"# canonical grandfathered surfaces (owner + deadline)\n"+
				"internal/api/handler.go\n")

		r := &report.Report{}
		ScanAPIInfrastructureImports(root, &policy.Policy{}, r)

		hits := filterByRule(r.Violations, "percheck_api_infrastructure_imports")
		if len(hits) != 0 {
			t.Fatalf("allowlisted_import_no_violation: want 0 hits (allowlist covers file), got %d (%v)", len(hits), summarizeViolations(r.Violations))
		}
	})

	t.Run("missing_allowlist_fail_closed", func(t *testing.T) {
		root := t.TempDir()
		writeFixtureFile(t, filepath.Join(root, "internal", "api", "handler.go"),
			"package api\n\nimport \"net/http\"\n\nfunc X() *http.Request { return nil }\n")
		// Deliberately do NOT create the allowlist — godlike/07
		// fail-closed: a missing canonical file is itself a
		// regression we want to surface on every default run.
		r := &report.Report{}
		ScanAPIInfrastructureImports(root, &policy.Policy{}, r)

		hits := filterByRule(r.Violations, "percheck_api_infrastructure_imports_allowlist_missing")
		if len(hits) != 1 {
			t.Fatalf("missing_allowlist_fail_closed: want exactly 1 fail-closed SeverityError, got %d (%v)", len(hits), summarizeViolations(r.Violations))
		}
		if hits[0].Severity != "error" {
			t.Fatalf("Severity = %q, want error", hits[0].Severity)
		}
		if !strings.Contains(hits[0].Note, "fail-closed") {
			t.Fatalf("Note missing fail-closed marker; got %q", hits[0].Note)
		}
	})

	t.Run("stale_allowlist_entry", func(t *testing.T) {
		root := t.TempDir()
		// api/ contains a clean handler — no infra-import on disk.
		writeFixtureFile(t, filepath.Join(root, "internal", "api", "handler.go"),
			"package api\n\nimport \"net/http\"\n\nfunc X() *http.Request { return nil }\n")
		// The allowlist points at a phantom file that no longer
		// exists in the api/ tree — godlike/08 zero-baseline
		// rule wants this surfaced.
		writeFixtureFile(t, filepath.Join(root, apiInfraImportAllowlistFile),
			"internal/api/legacy_drive_wrapper.go\n")

		r := &report.Report{}
		ScanAPIInfrastructureImports(root, &policy.Policy{}, r)

		// Empty allowlist file means no fail-closed. We expect
		// only the stale-entry warning.
		hits := filterByRule(r.Violations, "percheck_api_infrastructure_imports_allowlist_stale")
		if len(hits) != 1 {
			t.Fatalf("stale_allowlist_entry: want exactly 1 stale warning, got %d (%v)", len(hits), summarizeViolations(r.Violations))
		}
		if !strings.Contains(hits[0].Note, "stale") {
			t.Fatalf("Note missing stale marker; got %q", hits[0].Note)
		}
	})
}

// writeFixtureFile creates parent dirs as needed and writes content.
// Test helper extracted so the cases stay terse.
func writeFixtureFile(t *testing.T, rel, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(rel, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// filterByRule returns the subset of violations whose Rule field
// equals `rule`. Empty list if no matches. Used to make assertion
// sites unambiguous when percheck emits multiple rule families.
func filterByRule(violations []report.Violation, rule string) []report.Violation {
	out := []report.Violation{}
	for _, v := range violations {
		if v.Rule == rule {
			out = append(out, v)
		}
	}
	return out
}

// summarizeViolations is a debug aid: turns the violation slice
// into a single string summarizing rule + file for fast triage.
func summarizeViolations(violations []report.Violation) string {
	var b strings.Builder
	for _, v := range violations {
		b.WriteString("{rule=")
		b.WriteString(v.Rule)
		b.WriteString(",file=")
		b.WriteString(v.File)
		b.WriteString(",severity=")
		b.WriteString(v.Severity)
		b.WriteString("} ")
	}
	return b.String()
}
