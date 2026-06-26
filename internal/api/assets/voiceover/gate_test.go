package voiceover

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/voiceover
// (handler.go). Baseline only — bash Check 19 + the 28-entry
// grandfatherlist already enforce no infrastructure imports; this gate
// focuses on goroutines that bash Check 19 cannot catch. Per-area
// orchestrator patterns can be added after grep-verification during
// Wave 14 grandfathered-import drain.
var prohibitedPatterns = []gate.Prohibition{}

func TestStaticGate_NoVoiceoverAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
