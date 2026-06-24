package youtube

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/youtube.
// Baseline (no goroutines; bash Check 19 enforces no infrastructure
// imports) + grep-verified `yt-dlp` (3 matches across youtube_handlers.go
// + Wave 14-PR2 extraction scope).
var prohibitedPatterns = []gate.Prohibition{}

func TestStaticGate_NoYouTubeAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
