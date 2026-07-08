// Package qdrant — StockSearchAdapter adapts the application-level
// ports.StockSearchPort to the qdrant.Searcher primitives. For each
// scene, it embeds the scene text and searches Qdrant with a
// source=stock + lifecycle_state=ACTIVE filter, returning the top
// match.
//
// Per AGENTS.md Pattern 0, this is the ONLY place that imports both
// application-level scripts types and qdrant infra types (Hexagonal
// port pattern).
package search

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
)

// stockSearchAdapter implements ports.StockSearchPort against the
// canonical qdrant.Searcher with a source=stock +
// lifecycle_state=ACTIVE filter.
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

// Compile-time assertions (AGENTS.md Pattern 0).
//
// PR-POSTPROCESSOR-UNIFICATION-PHASE-3 Commit 3 (July 2026): the
// adapter now satisfies BOTH the canonical AssetSearchPort (added
// per the unification wave) AND the legacy StockSearchPort (embedded
// in stock_search_port.go during the 7-day soak). The SearchAssets
// method is the canonical entry point; SearchStock is preserved
// for back-compat with the 2 existing callers
// (scene/binder.go::BindClips + sqlite/catalog/repository.go::SearchStock)
// during the soak (forward-pointer PR-CLIPS-STOCK-PORT-RETIRE
// removes it).
var (
	_ ports.AssetSearchPort = (*stockSearchAdapter)(nil)
	_ ports.StockSearchPort = (*stockSearchAdapter)(nil)
)

// SearchAssets implements ports.AssetSearchPort (canonical).
//
// PR-POSTPROCESSOR-UNIFICATION-PHASE-3 Commit 3: the real
// implementation. The legacy SearchStock method (preserved for the
// 7-day soak) is now a thin wrapper that converts the legacy
// (query string, limit int) signature → unified AssetSearchQuery,
// calls this method, and converts the canonical AssetSearchHit
// results back to the legacy StockSearchHit shape. This direction
// (canonical-first) makes the forward-pointer
// PR-CLIPS-STOCK-PORT-RETIRE safer: after the soak, deleting
// SearchStock requires zero body rewrite because the embed + filter
// + result-conversion logic all live here in the canonical method.
//
// Stock-specific contract (preserved across the 7-day soak):
//   - Source is hard-coded to "stock" (vs clip's configurable Source)
//   - RequireActiveLifecycle is FORCED to true (vs clip's silent drop)
//     because the stock filter must-clause REQUIRES
//     lifecycle_state=ACTIVE — the field is asserted on the canonical
//     path so callers can't accidentally disable it.
//   - MinScore defaults to 0.3 (vs 0.5 for clips)
//   - Limit defaults to 5 (vs 20 for clips)
//   - DriveLink is populated from the payload (vs empty for clips per
//     QDRANT-001) — stock consumers need the DriveLink for direct
//     re-upload / preview flows.
//   - No workspace/tenant guard (stock is admin/reconcile path only;
//     the caller is trusted to provide a valid scope).
//
// Fail-closed guards (AGENTS.md godlike/07 NO-FAKE-AVAILABILITY):
//   - nil receiver or nil searcher → typed "searcher not configured"
//   - nil embedder → typed "embedder not configured"
//   - empty Query (whitespace-trimmed) → empty hit slice with nil
//     error (cheap pre-flight, mirrors the clip adapter pattern)
//   - caller-supplied Source != "stock" → Source is OVERRIDDEN to
//     "stock" (the stock adapter is canonical for the stock source;
//     a caller passing Source="youtube" gets a stock-only result set)
//   - caller-supplied RequireActiveLifecycle=false → field is
//     OVERRIDDEN to true (the lifecycle=ACTIVE filter is non-negotiable
//     for stock)
func (a *stockSearchAdapter) SearchAssets(ctx context.Context, q ports.AssetSearchQuery) ([]ports.AssetSearchHit, error) {
	if a == nil || a.searcher == nil {
		return nil, fmt.Errorf("stock search adapter: searcher not configured")
	}
	// Fast-path: empty query short-circuits BEFORE the embedder guard
	// (you don't need a wired embedder to return [] for an empty
	// query; this is a cheap pre-flight that mirrors the clip
	// adapter pattern).
	query := strings.TrimSpace(q.Query)
	if query == "" {
		return []ports.AssetSearchHit{}, nil
	}
	if a.embedder == nil {
		return nil, fmt.Errorf("stock search adapter: embedder not configured")
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 5
	}
	minScore := q.MinScore
	if minScore == 0 {
		minScore = 0.3
	}

	vec, err := a.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("stock search embed: %w", err)
	}

	results, err := a.searcher.Search(ctx, schema.SearchRequest{
		QueryVector: vec,
		VectorName:  a.vectorName,
		Limit:       limit,
		MinScore:    minScore,
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
	return convertStockAssetHits(results), nil
}

// SearchStock implements ports.StockSearchPort.
//
// PR-POSTPROCESSOR-UNIFICATION-PHASE-3 Commit 3: thin wrapper that
// converts the legacy (query string, limit int) signature → unified
// AssetSearchQuery, delegates to the canonical SearchAssets, then
// converts the canonical AssetSearchHit results back to the legacy
// StockSearchHit shape. This is the only stable surface for the 2
// existing callers (scene/binder.go::BindClips +
// sqlite/catalog/repository.go::SearchStock) during the 7-day soak;
// after forward-pointer PR-CLIPS-STOCK-PORT-RETIRE ships, this
// method is deleted and the callers migrate to SearchAssets
// directly.
func (a *stockSearchAdapter) SearchStock(ctx context.Context, query string, limit int) ([]ports.StockSearchHit, error) {
	canonicalHits, err := a.SearchAssets(ctx, ports.AssetSearchQuery{
		Query: query,
		// Source is hard-coded to "stock" by the adapter; any caller
		// value is ignored.
		// Category / MediaType / WorkspaceID / IsSystem are zero
		// values (stock is admin/reconcile path only, no tenant
		// scope).
		// RequireActiveLifecycle is FORCED to true by the adapter
		// (stock filter requires lifecycle_state=ACTIVE); any
		// caller value is overridden.
		Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ports.StockSearchHit, 0, len(canonicalHits))
	for _, h := range canonicalHits {
		out = append(out, ports.StockSearchHit{
			AssetID:   h.AssetID,
			Name:      h.Name,
			Source:    h.Source,
			DriveLink: h.DriveLink, // populated by convertAssetHits for stock path
			Score:     h.Score,
		})
	}
	return out, nil
}

// convertStockAssetHits maps infra-level schema.SearchResult → canonical
// AssetSearchHit. For the stock path, DriveLink IS populated (from
// payload "drive_link" or fallback "drive_url") — stock consumers
// need the DriveLink for direct re-upload / preview flows. This is
// the inverse of the clip adapter's convertClipAssetHits, which sets
// DriveLink="" per QDRANT-001.
//
// Per godlike/06 SSOT one-canonical-owner-per-fact: this function is
// the SOLE canonical owner of the stock-path wire-shape conversion.
// A future refactor of the SearchAssets method cannot silently break
// the DriveLink invariant — the conversion is co-located with the
// method that produces it.
//
// Named `convertStockAssetHits` (not `convertAssetHits`) per
// PR-POSTPROCESSOR-UNIFICATION-PHASE-3 Commit 3 to avoid name
// collision with `convertClipAssetHits` (the clip-path sibling
// function defined in clip_search_adapter.go, same package). The
// two functions have DIFFERENT DriveLink semantics (clip="" vs
// stock=populated from payload) and cannot share a single
// implementation; the explicit naming makes the per-path invariant
// visible at the call site.
func convertStockAssetHits(results []schema.SearchResult) []ports.AssetSearchHit {
	out := make([]ports.AssetSearchHit, 0, len(results))
	for _, r := range results {
		dl := payloadString(r.Payload, "drive_link")
		if dl == "" {
			dl = payloadString(r.Payload, "drive_url")
		}
		out = append(out, ports.AssetSearchHit{
			AssetID:   payloadString(r.Payload, "asset_id"),
			Name:      payloadString(r.Payload, "name"),
			Source:    payloadString(r.Payload, "source"),
			DriveLink: dl,
			Score:     r.Score,
		})
	}
	return out
}
