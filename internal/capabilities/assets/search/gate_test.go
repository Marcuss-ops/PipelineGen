package search

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/search
// (handler.go). Baseline (no goroutines; bash Check 19 enforces no
// infrastructure imports) + the grep-verified `search.NewService` +
// `NewSearchService` orchestrators (added 2026-06-24 followup).
// CAVEAT (substring anchor): `NewSearchService` matches any identifier
// containing the substring, not just the canonical orchestrator.
// Acceptable per current Go convention; tighten to a regex anchor via
// a gate.Walk enhancement if false positives emerge. See
// architecture/current.yaml::Wave 14 + arch check Check 19.
// Cross-ref: docs/migrations/api-infrastructure-imports-allowlist.txt
// (28 grandfathered-import entries as of Wave 14-PR3).
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	// Per-area orchestrator patterns (added 2026-06-24 followup, code-review
	// NIT-B): `search.NewService` + the broader `NewSearchService`
	// substring are canonical direct-orchestrator constructors; the API
	// layer must reach search via the typed registry in
	// internal/application/assets/realtime (or per-provider search facade),
	// not via direct construction here. Grep-verified: zero hits in
	// internal/api/* production code at HEAD, safe to enforce as
	// hard-fail patterns.
	{Name: "search.NewService direct construction", Pattern: "search.NewService"},
	{Name: "NewSearchService direct construction", Pattern: "NewSearchService"},
}

func TestStaticGate_NoSearchAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
