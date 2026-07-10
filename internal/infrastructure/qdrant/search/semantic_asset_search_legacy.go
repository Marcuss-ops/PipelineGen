// Package qdrant — semantic_asset_search_legacy.go — backward-compat wrappers.
//
// SearchClips and SearchStock are 7-day backward-compat wrappers that
// delegate to the canonical SearchAssets method. They survive until
// PR-CLIPS-STOCK-PORT-RETIRE retires them (deadline 2026-08-15).
package search

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
)

// SearchClips implements the legacy ClipSearchPort method.
//
// The runtime guard `a.kind != KindClip` makes accidental
// cross-pollination structurally impossible: a future agent that
// passes a stock-flavored adapter to a clip-flavored caller will
// see the typed error immediately, not a silently-wrong result
// set.
//
// 7-day backward-compat: this method survives until
// PR-CLIPS-STOCK-PORT-RETIRE retires it (deadline 2026-08-15).
func (a *semanticAssetSearchAdapter) SearchClips(ctx context.Context, q ports.ClipSearchQuery) ([]ports.ClipSearchHit, error) {
	if a.kind != KindClip {
		return nil, fmt.Errorf("clip path called on %s adapter (canonical kind=clip required; this is a runtime guard, not a soft fallback)", a.kind)
	}
	canonicalHits, err := a.SearchAssets(ctx, ports.AssetSearchQuery{
		Query:                  q.Query,
		Source:                 q.Source,
		Category:               q.Category,
		MediaType:              q.MediaType,
		WorkspaceID:            q.WorkspaceID,
		IsSystem:               q.IsSystem,
		Limit:                  q.Limit,
		MinScore:               q.MinScore,
		RequireActiveLifecycle: false, // clip path: CompileQdrantFilter already locks lifecycle=ACTIVE.
	})
	if err != nil {
		return nil, err
	}
	out := make([]ports.ClipSearchHit, 0, len(canonicalHits))
	for _, h := range canonicalHits {
		out = append(out, ports.ClipSearchHit{
			AssetID: h.AssetID,
			Name:    h.Name,
			Score:   h.Score,
			Source:  h.Source,
		})
	}
	return out, nil
}

// SearchStock implements the legacy StockSearchPort method.
//
// The runtime guard `a.kind != KindStock` makes accidental
// cross-pollination structurally impossible: a future agent that
// passes a clip-flavored adapter to a stock-flavored caller will
// see the typed error immediately, not a silently-wrong result
// set with the wrong filter + wrong default limit.
//
// 7-day backward-compat: this method survives until
// PR-CLIPS-STOCK-PORT-RETIRE retires it (deadline 2026-08-15).
func (a *semanticAssetSearchAdapter) SearchStock(ctx context.Context, query string, limit int) ([]ports.StockSearchHit, error) {
	if a.kind != KindStock {
		return nil, fmt.Errorf("stock path called on %s adapter (canonical kind=stock required; this is a runtime guard, not a soft fallback)", a.kind)
	}
	canonicalHits, err := a.SearchAssets(ctx, ports.AssetSearchQuery{
		Query: query,
		// Source is hard-coded to "stock" by the adapter; any caller value is ignored.
		// Category / MediaType / WorkspaceID / IsSystem are zero values (stock is admin/reconcile path only).
		// RequireActiveLifecycle is FORCED to true by the adapter (stock filter requires lifecycle_state=ACTIVE).
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
			DriveLink: h.DriveLink, // populated by convertAssetHitsByKind for KindStock
			Score:     h.Score,
		})
	}
	return out, nil
}
