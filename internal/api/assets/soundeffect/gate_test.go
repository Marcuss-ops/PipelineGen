package soundeffect

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/soundeffect.
// Baseline only (no goroutines; bash Check 19 enforces no infrastructure
// imports). PG-003 (June 2026) added a per-area rule mirroring the channels +
// images precedent: any reference to the infrastructure path itself fails
// the static gate, catching regressions before they reach the allowlist.
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	{Name: "no infrastructure imports", Pattern: "internal/infrastructure/"},
}

func TestStaticGate_NoSoundeffectAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
