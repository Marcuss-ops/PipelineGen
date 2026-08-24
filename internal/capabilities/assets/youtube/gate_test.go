package assets

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
//
// Blocco C1-Step 4 followup (June 2026, "stesso pattern di C1-Step 3
// artlist precedent"): the capability now exposes the canonical
// `ytsources.Build(deps Dependencies) (api.Descriptor, error)` entrypoint
// (mirrors Blocco C1-Step 3), so any direct `api.Registry.Register`
// call inside the package would bypass the composition root's
// capability_registry.go hoist site and break the Build contract
// (godlike/07 + future C2-A gate). Grep-verified at HEAD: zero hits in
// internal/api/assets/youtube production code.
var prohibitedPatterns = []gate.Prohibition{
	// Blocco C1-Step 4 followup (June 2026): direct `api.Registry.Register`
	// would bypass the canonical ytsources.Build contract.
	{Name: "no direct api.Registry.Register (Blocco C1-Step 4)", Pattern: "api.Registry.Register"},
	// Per-area hard-fail rule that mirrors the channels + images +
	// soundeffect + artlist precedent. Any reference to the
	// infrastructure path fails the gate so future regressions
	// surface before they reach the allowlist.
	{Name: "no infrastructure imports", Pattern: "internal/infrastructure/"},
}

func TestStaticGate_NoYouTubeAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
