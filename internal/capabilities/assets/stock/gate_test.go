package stock

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/stock.
// Baseline (no goroutines; bash Check 19 enforces no infrastructure
// imports) + the grep-verified `stockpipeline.NewProductionStockPipeline` direct
// orchestrator (added 2026-06-24 followup). See
// architecture/current.yaml::Wave 14 + arch check Check 19.
// Cross-ref: docs/migrations/api-infrastructure-imports-allowlist.txt
// (28 grandfathered-import entries as of Wave 14-PR3).
//
// HTTP HANDLER RETIRED (godlike/07 NO-FAKE-AVAILABILITY, July 2026):
// the `stock.NewHandler` prohibition was retired because the HTTP
// handler itself was deleted — no production code can construct
// what doesn't exist, and re-introducing the handler in the future
// will require re-adding the prohibition then.
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	// Per-area orchestrator pattern (added 2026-06-24 followup, code-review
	// NIT-B): `stockpipeline.NewProductionStockPipeline` is the canonical direct-
	// orchestrator constructor; the API layer must reach the stock
	// pipeline via the StockBundle wired in internal/app/composition.go,
	// not via direct construction here. Grep-verified: zero hits in
	// internal/api/* production code at HEAD, safe to enforce as
	// hard-fail pattern.
	{Name: "stockpipeline.NewProductionStockPipeline direct construction", Pattern: "stockpipeline.NewProductionStockPipeline"},
}

func TestStaticGate_NoStockAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
