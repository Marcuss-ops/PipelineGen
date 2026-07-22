package ports

import "context"

// StockSearchPort is the legacy narrow port for semantic stock
// footage discovery, consumed by the visual_planning postprocessor.
//
// PR-POSTPROCESSOR-UNIFICATION-PHASE-3 Commit 3 (July 2026):
// StockSearchPort EMBEDS the canonical AssetSearchPort AND
// retains the legacy SearchStock method. This is the Go-idiomatic
// "soft migration" pattern: callers see both the new SearchAssets
// (from the embedded interface) and the legacy SearchStock methods
// during the 7-day soak (FASE-2.1-VOICE-FREEZE discipline).
//
// After 7 days, forward-pointer PR-CLIPS-STOCK-PORT-RETIRE will
// remove the legacy SearchStock method and convert this to:
//
//	type StockSearchPort = AssetSearchPort
//
// (true Go type alias for canonical surface deduplication).
type StockSearchPort interface {
	AssetSearchPort

	// SearchStock embeds the query text and performs an ANN search
	// over stock-indexed assets. Returns up to limit results ranked
	// by cosine similarity (highest first). An empty query returns
	// an empty slice with nil error.
	//
	// Stock-specific contract (preserved during 7-day soak):
	// - source is always "stock" (hard-coded in the adapter)
	// - MinScore defaults to 0.3 (vs 0.5 for clips)
	// - Limit defaults to 5 (vs 20 for clips)
	// - lifecycle_state=ACTIVE is REQUIRED (RequireActiveLifecycle=true)
	// - DriveLink is populated from the payload (vs empty for clips per QDRANT-001)
	// - No workspace/tenant guard (stock is admin/reconcile path only)
	SearchStock(ctx context.Context, query string, limit int) ([]StockSearchHit, error)
}

// StockSearchHit is a single stock asset match from the vector search.
type StockSearchHit struct {
	AssetID   string  `json:"asset_id"`
	Name      string  `json:"name"`
	Source    string  `json:"source"`
	DriveLink string  `json:"drive_link"`
	Score     float64 `json:"score"`
}
