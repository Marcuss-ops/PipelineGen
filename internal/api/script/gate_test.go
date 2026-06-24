package script

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/archcheck/gate"
)

// prohibitedPatterns is the per-area prohibition list owned by this
// call site. Mirrors the contract Agente 4 (June 2026) requires for the
// HTTP transport layer: binding + DTO conversion only — no concrete
// infrastructure adapters, no goroutines, no business orchestrators.
//
// `internal/infrastructure` is intentionally NOT in this list: bash
// Check 19 (`scripts/ci-architectural-checks.sh`) + the 28-entry
// grandfatherlist in `docs/migrations/api-infrastructure-imports-allowlist.txt`
// already enforces it. This gate focuses on transport discipline
// (goroutines, business orchestrators, direct service constructors)
// that bash Check 19 cannot catch.
var prohibitedPatterns = []gate.Prohibition{
	{Name: "scriptGenSem channel", Pattern: "scriptGenSem"},
	{Name: "RegisterJobHandlers in API", Pattern: "RegisterJobHandlers"},
	{Name: "CatalogJobServiceImpl alias", Pattern: "CatalogJobServiceImpl"},
	{Name: "CurationJobServiceImpl alias", Pattern: "CurationJobServiceImpl"},
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	{Name: "BuildMetadataLanguages in API", Pattern: "BuildMetadataLanguages"},
	{Name: "GenerateVideoMetadata in API", Pattern: "GenerateVideoMetadata"},
	{Name: "drive.DocClient in API", Pattern: "drive.DocClient"},
	{Name: "ollama.Generator in API", Pattern: "ollama.Generator"},
	{Name: "config.Config in API", Pattern: "config.Config"},
	{Name: "BatchService direct ref", Pattern: "BatchService"},
	{Name: "NewScenesService in API", Pattern: "NewScenesService"},
	{Name: "NewDocumentsService in API", Pattern: "NewDocumentsService"},
}

// TestStaticGate_NoConcreteInfrastructureInTransport enforces the
// script-package architectural contract via the shared
// pkg/archcheck/gate machinery. Per-violation failures surface in
// the test report (gate.Walk calls t.Errorf per match); the test
// halts via t.Fatalf when the total is non-zero (real-fail, not
// log-only). This is the SHIP-BLOCKER fix flagged by Agente 5 —
// the prior t.Logf-only version silently absorbed violations.
func TestStaticGate_NoConcreteInfrastructureInTransport(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
