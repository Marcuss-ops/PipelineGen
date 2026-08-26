package structure

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// TestScanLegacyHotspotGrowthRatchetsAllRegisteredHotspots proves the
// generalized (P0-LEGACY-RATCHET) behavior: a registered hotspot under a
// *target* root that sits below the global cap still fails closed when it grows
// past its committed baseline. Before the 2026-08-26 generalization the scan
// filtered by legacy-root membership and returned early when no legacy roots
// existed, so growth like stockpipeline 51→63 was invisible.
func TestScanLegacyHotspotGrowthRatchetsAllRegisteredHotspots(t *testing.T) {
	root := t.TempDir()
	writeRegistryJSON(t, root, `{
  "version": 1,
  "hotspots": [{
    "path": "internal/capabilities/assets/providers/stock/stockpipeline",
    "owner": "stock pipeline orchestration",
    "deadline": "2026-09-30",
    "baseline_files": 2,
    "target_packages": ["internal/capabilities/assets/stock/ingest"]
  }],
  "root_migrations": []
}`)
	dir := filepath.Join(root, "internal", "capabilities", "assets", "providers", "stock", "stockpipeline")
	writeGoFixture(t, dir, "a.go")
	writeGoFixture(t, dir, "b.go")
	writeGoFixture(t, dir, "c.go")

	r := &report.Report{}
	ScanLegacyHotspotGrowth(root, &policy.Policy{}, r)

	if len(r.Violations) != 1 {
		t.Fatalf("expected one growth violation, got %#v", r.Violations)
	}
	v := r.Violations[0]
	if v.Rule != legacyHotspotRatchetRule || v.MatchedRule != "legacy_hotspot_growth" || v.Severity != "error" {
		t.Fatalf("unexpected violation shape: %#v", v)
	}
	if v.Package != "internal/capabilities/assets/providers/stock/stockpipeline" || v.ActualCount != 3 || v.AllowedCount != 2 {
		t.Fatalf("unexpected violation counts: %#v", v)
	}
}

// TestScanLegacyHotspotGrowthAllowsWithinBaseline proves a hotspot at or below
// its baseline does not ratchet.
func TestScanLegacyHotspotGrowthAllowsWithinBaseline(t *testing.T) {
	root := t.TempDir()
	writeRegistryJSON(t, root, `{
  "version": 1,
  "hotspots": [{
    "path": "internal/platform/delivery",
    "owner": "delivery canonical platform owner",
    "deadline": "2026-12-31",
    "baseline_files": 2,
    "target_packages": ["internal/platform/delivery/signing"]
  }],
  "root_migrations": []
}`)
	dir := filepath.Join(root, "internal", "platform", "delivery")
	writeGoFixture(t, dir, "a.go")
	writeGoFixture(t, dir, "b.go")

	r := &report.Report{}
	ScanLegacyHotspotGrowth(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Fatalf("within-baseline hotspot must not ratchet, got %#v", r.Violations)
	}
}

func writeRegistryJSON(t *testing.T, root, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash("architecture/package_hotspots.json"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeGoFixture(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("package stockpipeline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
