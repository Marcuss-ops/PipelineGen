package youtube

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/youtube.
// Baseline (no goroutines; bash Check 19 enforces no infrastructure
// imports) + grep-verified `yt-dlp` (3 matches across youtube_handlers.go
// + Wave 14-PR2 extraction scope).
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	{Name: "yt-dlp exec reach-through", Pattern: "yt-dlp"},
}

func TestStaticGate_NoYouTubeAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
