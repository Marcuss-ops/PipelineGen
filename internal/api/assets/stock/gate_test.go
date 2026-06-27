package stock

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/scripts/archcheck/gate"
)

// prohibitedPatterns is the per-area list for internal/api/assets/stock
// (handler.go). Baseline (no goroutines; bash Check 19 enforces no
// infrastructure imports) + the grep-verified `stockpipeline.NewService`
// + `stock.NewHandler` orchestrators (added 2026-06-24 followup). See
// architecture/current.yaml::Wave 14 + arch check Check 19.
// Cross-ref: docs/migrations/api-infrastructure-imports-allowlist.txt
// (28 grandfathered-import entries as of Wave 14-PR3).
var prohibitedPatterns = []gate.Prohibition{
	{Name: "unsafe goroutines (go func)", Pattern: "go func"},
	{Name: "unsafe goroutines (SafeGo)", Pattern: "SafeGo"},
	// Per-area orchestrator patterns (added 2026-06-24 followup, code-review
	// NIT-B): `stockpipeline.NewService` + `stock.NewHandler` are the
	// canonical direct-orchestrator constructors; the API layer must reach
	// the stock pipeline via the StockBundle wired in
	// internal/app/composition.go, not via direct construction here.
	// Grep-verified: zero hits in internal/api/* production code at HEAD,
	// safe to enforce as hard-fail patterns.
	{Name: "stockpipeline.NewService direct construction", Pattern: "stockpipeline.NewService"},
	{Name: "stock.NewHandler direct construction", Pattern: "stock.NewHandler"},
}

func TestStaticGate_NoStockAPIInfrastructureLeaks(t *testing.T) {
	gate.Walk(t, gate.Config{
		Root:               ".",
		ProhibitedPatterns: prohibitedPatterns,
	})
}
