// Package qdrant — StockSearchAdapter adapts the application-level
// ports.StockSearchPort to the qdrant.Searcher primitives. For each
// scene, it embeds the scene text and searches Qdrant with a
// source=stock filter, returning the top match.
//
// Per AGENTS.md Pattern 0, this is the ONLY place that imports
// both application-level scripts types and qdrant infra types
// (Hexagonal port pattern).
package qdrant

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
)

// stockSearchAdapter implements ports.StockSearchPort against the
// canonical qdrant.Searcher with a source=stock filter.
type stockSearchAdapter struct {
	searcher   *Searcher
	embedder   TextEmbedder
	vectorName string
	log        *zap.Logger
}

// NewStockSearchAdapter constructs the StockSearchPort implementation.
// embedder is required (text → vector). vectorName is the dense vector
// channel (e.g. "text"). Both are supplied by the composition root.
func NewStockSearchAdapter(searcher *Searcher, embedder TextEmbedder, vectorName string, log *zap.Logger) ports.StockSearchPort {
	return &stockSearchAdapter{
		searcher:   searcher,
		embedder:   embedder,
		vectorName: vectorName,
		log:        log,
	}
}

// Compile-time assertion (AGENTS.md Pattern 0).
var _ ports.StockSearchPort = (*stockSearchAdapter)(nil)

// SearchStock embeds query, searches Qdrant with source=stock filter.
func (a *stockSearchAdapter) SearchStock(ctx context.Context, query string, limit int) ([]ports.StockSearchHit, error) {
	if a == nil || a.searcher == nil {
		return nil, fmt.Errorf("stock search adapter: searcher not configured")
	}
	if a.embedder == nil {
		return nil, fmt.Errorf("stock search adapter: embedder not configured")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return []ports.StockSearchHit{}, nil
	}
	if limit <= 0 {
		limit = 5
	}

	vec, err := a.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("stock search embed: %w", err)
	}

	results, err := a.searcher.Search(ctx, SearchRequest{
		QueryVector: vec,
		VectorName:  a.vectorName,
		Limit:       limit,
		MinScore:    0.3,
		Filter: map[string]interface{}{
			"must": []map[string]interface{}{
				{"key": "source", "match": map[string]interface{}{"value": "stock"}},
				{"key": "lifecycle_state", "match": map[string]interface{}{"value": "ACTIVE"}},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("stock search: %w", err)
	}

	return convertStockHits(results), nil
}

func convertStockHits(results []SearchResult) []ports.StockSearchHit {
	out := make([]ports.StockSearchHit, 0, len(results))
	for _, r := range results {
		dl := payloadString(r.Payload, "drive_link")
		if dl == "" {
			dl = payloadString(r.Payload, "drive_url")
		}
		out = append(out, ports.StockSearchHit{
			AssetID:   payloadString(r.Payload, "asset_id"),
			Name:      payloadString(r.Payload, "name"),
			Source:    payloadString(r.Payload, "source"),
			DriveLink: dl,
			Score:     r.Score,
		})
	}
	return out
}
