package structure

import (
	"testing"
	"time"

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
