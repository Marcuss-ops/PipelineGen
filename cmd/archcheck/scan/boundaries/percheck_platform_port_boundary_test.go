package boundaries

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func TestScanPlatformPortBoundaryRejectsBusinessPackageImport(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "platform", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const source = `package demo
import (
 "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
 "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
)
`
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := swapPlatformScriptsInventory(nil)
	defer restore()

	var r report.Report
	ScanPlatformPortBoundary(root, nil, &r)
	if len(r.Violations) != 1 {
		t.Fatalf("expected exactly the new-importer violation, got %#v", r.Violations)
	}
	if r.Violations[0].MatchedRule != "platform_capability_business_import" {
		t.Fatalf("unexpected matched rule %q", r.Violations[0].MatchedRule)
	}
}

func TestScanPlatformPortBoundaryAcceptsKernelContractAndInventory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "platform", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// One file on the inventory (recorded debt) and one importing only the
	// kernel contract (the migrated shape).
	const inventoried = `package demo
import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
`
	const migrated = `package demo
import "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
`
	if err := os.WriteFile(filepath.Join(dir, "debt.go"), []byte(inventoried), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "clean.go"), []byte(migrated), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := swapPlatformScriptsInventory(map[string]string{
		"internal/platform/demo/debt.go": "owner=test; removal_trigger=never",
	})
	defer restore()

	var r report.Report
	ScanPlatformPortBoundary(root, nil, &r)
	if len(r.Violations) != 0 {
		t.Fatalf("expected no violations, got %#v", r.Violations)
	}
}

func TestScanPlatformPortBoundaryRejectsStaleInventoryEntry(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "platform", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const migrated = `package demo
import "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
`
	if err := os.WriteFile(filepath.Join(dir, "clean.go"), []byte(migrated), 0o600); err != nil {
		t.Fatal(err)
	}
	// The file no longer imports the forbidden package: keeping its entry would
	// authorise a debt that no longer exists.
	restore := swapPlatformScriptsInventory(map[string]string{
		"internal/platform/demo/clean.go": "owner=test; removal_trigger=never",
	})
	defer restore()

	var r report.Report
	ScanPlatformPortBoundary(root, nil, &r)
	if len(r.Violations) != 1 {
		t.Fatalf("expected one stale-inventory violation, got %#v", r.Violations)
	}
	if r.Violations[0].Rule != platformPortInventoryStaleRule {
		t.Fatalf("unexpected rule %q", r.Violations[0].Rule)
	}
}

func TestScanPlatformPortBoundaryIgnoresTestFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "platform", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const source = `package demo
import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
`
	if err := os.WriteFile(filepath.Join(dir, "bad_test.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := swapPlatformScriptsInventory(nil)
	defer restore()

	var r report.Report
	ScanPlatformPortBoundary(root, nil, &r)
	if len(r.Violations) != 0 {
		t.Fatalf("test files are out of scope for the adapter gate, got %#v", r.Violations)
	}
}

func swapPlatformScriptsInventory(next map[string]string) func() {
	previous := platformCapabilityScriptsInventory
	platformCapabilityScriptsInventory = next
	return func() { platformCapabilityScriptsInventory = previous }
}
