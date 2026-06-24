package clips

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/clips.
// Baseline (no goroutines; bash Check 19 enforces no infrastructure
// imports) + grep-verified presence of `BulkUpload` in 4 clips/*.go
// files (Wave 14-PR2 grandfathered set).
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	{Name: "BulkUpload direct ref", Pattern: "BulkUpload"},
}

func TestStaticGate_NoClipsAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
