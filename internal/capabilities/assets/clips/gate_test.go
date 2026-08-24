package assets

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/clips.
// Baseline (no goroutines; bash Check 19 enforces no infrastructure
// imports) + grep-verified presence of `BulkUpload` in 4 clips/*.go
// files (Wave 14-PR2 grandfathered set).
var prohibitedPatterns = []gate.Prohibition{}

func TestStaticGate_NoClipsAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
