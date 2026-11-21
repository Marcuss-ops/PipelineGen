package boundaries

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func scanCapabilitySQL(t *testing.T, root string) *report.Report {
	t.Helper()
	r := &report.Report{}
	ScanCapabilitySQLBan(root, &policy.Policy{}, r)
	return r
}

func capabilitySQLViolations(r *report.Report, rule string) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == rule {
			out = append(out, v)
		}
	}
	return out
}

func withCapabilitySQLInventory(t *testing.T, next map[string]string) {
	t.Helper()
	previous := capabilitySQLInventory
	capabilitySQLInventory = next
	t.Cleanup(func() { capabilitySQLInventory = previous })
}

func TestScanCapabilitySQLBanFlagsImportsAndDDL(t *testing.T) {
	withCapabilitySQLInventory(t, map[string]string{})
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/bad/import.go", `package bad

import "database/sql"

var _ *sql.DB
`)
	writeGoFile(t, tmp, "internal/capabilities/bad/ddl.go", "package bad\n\nconst ddl = `CREATE TABLE IF NOT EXISTS x (id TEXT)`\n")
	got := capabilitySQLViolations(scanCapabilitySQL(t, tmp), capabilitySQLBanRule)
	if len(got) != 2 {
		t.Fatalf("violations = %d, want 2: %+v", len(got), got)
	}
}

func TestScanCapabilitySQLBanHonoursInventoryAndIgnoresProse(t *testing.T) {
	const rel = "internal/capabilities/legacy/io.go"
	withCapabilitySQLInventory(t, map[string]string{rel: "owner=test; removal_trigger=test"})
	tmp := t.TempDir()
	writeGoFile(t, tmp, rel, `package legacy

import "database/sql"

var _ *sql.DB
`)
	writeGoFile(t, tmp, "internal/capabilities/clean/prose.go", `package clean

// This package used to run CREATE TABLE statements; the DDL now lives in migrations/.
func f() {}
`)
	r := scanCapabilitySQL(t, tmp)
	if got := capabilitySQLViolations(r, capabilitySQLBanRule); len(got) != 0 {
		t.Fatalf("inventoried import and prose comment must not fire, got %+v", got)
	}
	if got := capabilitySQLViolations(r, capabilitySQLInventoryStaleRule); len(got) != 0 {
		t.Fatalf("stale rule fired for a live inventory entry: %+v", got)
	}
}

func TestScanCapabilitySQLBanFlagsStaleInventory(t *testing.T) {
	withCapabilitySQLInventory(t, map[string]string{
		"internal/capabilities/clean/now.go": "owner=test; removal_trigger=test",
	})
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/clean/now.go", "package clean\n")
	got := capabilitySQLViolations(scanCapabilitySQL(t, tmp), capabilitySQLInventoryStaleRule)
	if len(got) != 1 {
		t.Fatalf("stale violations = %d, want 1: %+v", len(got), got)
	}
}

func TestScanCapabilitySQLBanRepoTreeIsClean(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("repository root not resolvable: %v", err)
	}
	r := scanCapabilitySQL(t, root)
	if got := capabilitySQLViolations(r, capabilitySQLBanRule); len(got) != 0 {
		t.Fatalf("capability SQL ban returned: %+v", got)
	}
	if got := capabilitySQLViolations(r, capabilitySQLInventoryStaleRule); len(got) != 0 {
		t.Fatalf("capability SQL inventory is stale: %+v", got)
	}
}
