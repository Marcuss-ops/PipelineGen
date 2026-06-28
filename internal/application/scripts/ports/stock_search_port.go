package ports

import "context"

// StockSearchPort is the narrow port for semantic stock footage
// discovery, consumed by the stock_association postprocessor.
//
// Production adapter: qdrant.StockSearchAdapter wraps
// Searcher.SearchByText with a source=stock filter.
type StockSearchPort interface {
	// SearchStock embeds the query text and performs an ANN search
	// over stock-indexed assets. Returns up to limit results ranked
	// by cosine similarity (highest first). An empty query returns
	// an empty slice with nil error.
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
