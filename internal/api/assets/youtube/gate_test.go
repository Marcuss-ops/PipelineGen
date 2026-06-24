package youtube

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/youtube.
// Baseline (no goroutines; bash Check 19 enforces no infrastructure
// imports) + grep-verified `yt-dlp` (3 matches across youtube_handlers.go
// + Wave 14-PR2 extraction scope) + the `youtubeadapter.NewAdapter` +
// `youtube.NewService` orchestrators (added 2026-06-24 followup).
// Note: youtube_handlers.go's `// Check the ytdlp binary` comment trip
// on the `yt-dlp` substring; flagged as a known comment-shape issue
// to be drained in Wave 14 youtube surface cleanup (tracked in
// architecture/migration.yaml). See also arch check Check 19.
// Cross-ref: docs/migrations/api-infrastructure-imports-allowlist.txt
// (28 grandfathered-import entries as of Wave 14-PR3).
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	{Name: "yt-dlp exec reach-through", Pattern: "yt-dlp"},
	// Per-area orchestrator patterns (added 2026-06-24 followup, code-review
	// NIT-B): `youtubeadapter.NewAdapter` + `youtube.NewService` are the
	// canonical direct-orchestrator constructors; the API layer must reach
	// the youtube provider via the typed registry in
	// internal/application/assets/providers, not via direct construction
	// here. Grep-verified: zero hits in internal/api/* production code
	// at HEAD, safe to enforce as hard-fail patterns.
	{Name: "youtubeadapter.NewAdapter direct construction", Pattern: "youtubeadapter.NewAdapter"},
	{Name: "youtube.NewService direct construction", Pattern: "youtube.NewService"},
}

func TestStaticGate_NoYouTubeAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
