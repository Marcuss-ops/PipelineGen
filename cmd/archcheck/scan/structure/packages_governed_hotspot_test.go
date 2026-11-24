package structure

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func TestAppendPackageHotspotResultAcceptsGovernedDebt(t *testing.T) {
	r := &report.Report{}
	hotspots := map[string]packageHotspot{
		"internal/app": {
			Path:           "internal/app",
			Owner:          "composition root",
			Deadline:       "2026-09-30",
			BaselineFiles:  127,
			TargetPackages: []string{"internal/app/wiring"},
		},
	}

	appendPackageHotspotResult(
		r,
		"internal/app",
		120,
		40,
		hotspots,
		true,
		time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC),
	)
	if len(r.Violations) != 0 {
		t.Fatalf("governed hotspot within baseline and deadline must not fail strict mode: %#v", r.Violations)
	}
}

func TestScanPackagesForModeChecksRegisteredHotspotBelowCap(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "internal", "app", "wiring")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(pkgDir, name), []byte("package wiring\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeHotspotRegistry(t, root, `{"version":1,"hotspots":[{"path":"internal/app/wiring","owner":"test","deadline":"2027-03-31","baseline_files":1,"target_packages":["internal/app/modules/script"]}],"root_migrations":[]}`)

	rep := &report.Report{}
	ScanPackagesForMode(root, &policy.Policy{MaxFilesPerPackage: 65, MaxLinesPerFile: 1000}, rep, map[string]int{}, true)
	if !hasMatchedRule(rep, "package_hotspot_growth") {
		t.Fatalf("registered hotspot below the global cap must ratchet; violations=%#v", rep.Violations)
	}
}

func TestScanPackagesForModeEnforcesPackageLineBudget(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "internal", "app", "wiring")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(pkgDir, name), []byte("package wiring\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeHotspotRegistry(t, root, `{"version":1,"hotspots":[],"line_budgets":[{"path":"internal/app/wiring","max_loc":1,"owner":"test","deadline":"2027-03-31"}],"root_migrations":[]}`)

	rep := &report.Report{}
	ScanPackagesForMode(root, &policy.Policy{MaxFilesPerPackage: 65, MaxLinesPerFile: 1000}, rep, map[string]int{}, true)
	if !hasMatchedRule(rep, "package_line_budget_growth") {
		t.Fatalf("line budget overrun must fail; violations=%#v", rep.Violations)
	}
}

func TestScanPackagesForModeAcceptsWithinPackageLineBudget(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "internal", "app", "wiring")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(pkgDir, name), []byte("package wiring\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeHotspotRegistry(t, root, `{"version":1,"hotspots":[],"line_budgets":[{"path":"internal/app/wiring","max_loc":2,"owner":"test","deadline":"2027-03-31"}],"root_migrations":[]}`)

	rep := &report.Report{}
	ScanPackagesForMode(root, &policy.Policy{MaxFilesPerPackage: 65, MaxLinesPerFile: 1000}, rep, map[string]int{}, true)
	for _, v := range rep.Violations {
		if v.MatchedRule == "package_line_budget_growth" {
			t.Fatalf("within-budget package must not fail: %#v", v)
		}
	}
}

func writeHotspotRegistry(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, "architecture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package_hotspots.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasMatchedRule(r *report.Report, rule string) bool {
	for _, v := range r.Violations {
		if v.MatchedRule == rule {
			return true
		}
	}
	return false
}

func TestAppendPackageHotspotResultRejectsGrowth(t *testing.T) {
	r := &report.Report{}
	hotspots := map[string]packageHotspot{
		"internal/app": {
			Path:           "internal/app",
			Owner:          "composition root",
			Deadline:       "2026-09-30",
			BaselineFiles:  127,
			TargetPackages: []string{"internal/app/wiring"},
		},
	}

	appendPackageHotspotResult(
		r,
		"internal/app",
		128,
		40,
		hotspots,
		true,
		time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC),
	)
	if len(r.Violations) != 1 || r.Violations[0].MatchedRule != "package_hotspot_growth" || r.Violations[0].Severity != "error" {
		t.Fatalf("growth beyond registered baseline must fail closed: %#v", r.Violations)
	}
}
