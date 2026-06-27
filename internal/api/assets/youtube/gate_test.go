package youtube

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/youtube.
// Baseline (no goroutines; bash Check 19 enforces no infrastructure
// imports) + grep-verified `yt-dlp` (3 matches across youtube_handlers.go
// + Wave 14-PR2 extraction scope). PG-003 (June 2026) adds an explicit
// infrastructure-import rule that mirrors the channels + images
// precedent, catching regressions before they reach the allowlist.
var prohibitedPatterns = []gate.Prohibition{
	{Name: "no infrastructure imports", Pattern: "internal/infrastructure/"},
}

func TestStaticGate_NoYouTubeAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
