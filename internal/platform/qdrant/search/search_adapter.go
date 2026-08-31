// Package qdrant — SearchAdapter bridges the infrastructure-level qdrant.Searcher
// to the application-level search.VectorStorePort interface. Per AGENTS.md Pattern 0
// (Port abstraction layer), this adapter is the ONLY place that imports both
// qdrant types and application-level search types.
//
// QDRANT-003: The adapter converts application-layer request/response DTOs
// (VectorSearchRequest, schema.HybridSearchRequest, VectorSearchResult) into the
// canonical qdrant types (schema.SearchRequest, schema.HybridSearchRequest, schema.SearchResult)
// and back.
//
// PR 5 (June 2026, fix/qdrant-tenant-scope): both Search and HybridSearch
// route their filter construction through the canonical
// CompileQdrantFilter (declared in filter_compiler.go). The previous
// inline `buildLifecycleAwareFilter` helper was DELETED because both
// callers migrated to CompileQdrantFilter in this same PR and the
// pre-PR5 hand-rolled logic was the source of the curate-path
// workspace-omission drift the verdict §8 flagged. The deletion is
// the "Code Hygiene: remove unused variables, functions, and files
// as a result of your changes" rule from AGENTS.md applied.
package search

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// SearchAdapter adapts qdrant.Searcher to search.VectorStorePort.
type SearchAdapter struct {
	searcher   *Searcher
	assetStore indexing.AssetStore
	log        *zap.Logger
}

// NewSearchAdapter creates a search.VectorStorePort implementation backed
// by Qdrant for candidate retrieval and SQLite for canonical result data.
// assetStore is required for real searches; Qdrant payload fields other
// than asset_id are intentionally ignored during hydration.
func NewSearchAdapter(searcher *Searcher, assetStore indexing.AssetStore, log *zap.Logger) *SearchAdapter {
	return &SearchAdapter{searcher: searcher, assetStore: assetStore, log: log}
}

// Compile-time assertion.
var _ appsearch.VectorStorePort = (*SearchAdapter)(nil)

// Search converts an application-level VectorSearchRequest into a qdrant
// schema.SearchRequest, delegates to the Searcher, and converts results back.
//
// PR 5 (June 2026, fix/qdrant-tenant-scope): filter construction routes
// through CompileQdrantFilter so the workspace + lifecycle invariants
// cannot drift between Search and HybridSearch. The previous inline
// `buildLifecycleAwareFilter` was deleted.
//
// PR 1 (June 2026, Lifecycle state SSOT): the canonical lifecycle_state
// filter is {\"ACTIVE\"} only. Pre-PR1 the waterfall was {\"active\",
// \"searchable\"} — both legacy values are pruned by migration 101 and
// no production code path writes a non-ACTIVE searchable value anymore.
func (a *SearchAdapter) Search(ctx context.Context, req appsearch.VectorSearchRequest) ([]appsearch.VectorSearchResult, error) {
	if a.searcher == nil {
		return nil, fmt.Errorf("qdrant searcher not configured")
	}

	filter, err := CompileQdrantFilter(
		appsearch.SearchScope{
			WorkspaceID: req.WorkspaceID,
			IsSystem:    req.IsSystem,
		},
		appsearch.AssetFilter{
			Source:         req.Source,
			Category:       req.Category,
			MediaType:      req.MediaType,
			Language:       req.Language,
			LifecycleState: req.LifecycleState,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("qdrant search: compile filter: %w", err)
	}

	qReq := schema.SearchRequest{
		QueryVector: req.QueryVector,
		VectorName:  req.VectorName,
		Limit:       req.Limit,
		MinScore:    req.MinScore,
		Filter:      filter,
	}

	results, err := a.searcher.Search(ctx, qReq)
	if err != nil {
		return nil, fmt.Errorf("qdrant search: %w", err)
	}

	return a.hydrateSearchResults(ctx, results)
}

// HybridSearch converts an application-level schema.HybridSearchRequest into a
// qdrant schema.HybridSearchRequest, delegates to the Searcher, and converts back.
//
// PR 5 (June 2026, fix/qdrant-tenant-scope): filter construction routes
// through the SAME CompileQdrantFilter call as Search, so the workspace +
// lifecycle invariants are uniform across both retrieval paths. The
// pre-PR5 curate-path bug (silent workspace-omission) was inherited by
// hybrid because the curate path was the diagnostic surface that first
// surfaced the issue; canonicalising both on CompileQdrantFilter closes
// the inheritance gap.
//
// PR 2 (June 2026, fix/qdrant-bm25-indexing): live retrieval hands the
// raw query text via SparseText; Qdrant server-side BM25 inference
// handles tokenization + projection. The deprecated client-side raw
// vector (SparseVector) is kept ONLY for diagnostic / bulk-from-csv
// paths. The adapter threads both fields straight through to the
// infra-level schema.HybridSearchRequest envelope.
//
// PR 1 (June 2026, Lifecycle state SSOT): same canonical ACTIVE-only
// filter as Search() so hybrid results never include DELETED/STAGING/
// PROCESSING/DELETE_PENDING/ERROR points.
func (a *SearchAdapter) HybridSearch(ctx context.Context, req appsearch.HybridSearchRequest) ([]appsearch.VectorSearchResult, error) {
	if a.searcher == nil {
		return nil, fmt.Errorf("qdrant searcher not configured")
	}

	filter, err := CompileQdrantFilter(
		appsearch.SearchScope{
			WorkspaceID: req.WorkspaceID,
			IsSystem:    req.IsSystem,
		},
		appsearch.AssetFilter{
			Source:         req.Source,
			Category:       req.Category,
			MediaType:      req.MediaType,
			Language:       req.Language,
			LifecycleState: req.LifecycleState,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("qdrant hybrid search: compile filter: %w", err)
	}

	var sparseVec *schema.SparseQueryVector
	if req.SparseVector != nil {
		sparseVec = &schema.SparseQueryVector{
			Indices: req.SparseVector.Indices,
			Values:  req.SparseVector.Values,
		}
	}

	qReq := schema.HybridSearchRequest{
		DenseVector:       req.DenseVector,
		DenseVectorName:   req.DenseVectorName,
		SparseVectorName:  req.SparseVectorName,
		SparseText:        req.SparseText,
		SparseModel:       req.SparseModel,
		SparseQueryVector: sparseVec,
		Limit:             req.Limit,
		MinScore:          req.MinScore,
		Filter:            filter,
	}

	results, err := a.searcher.HybridSearch(ctx, qReq)
	if err != nil {
		return nil, fmt.Errorf("qdrant hybrid search: %w", err)
	}

	return a.hydrateSearchResults(ctx, results)
}

// ── Canonical hydration boundary ─────────────────────────────────────

// hydrateSearchResults converts Qdrant hits into API results by using
// Qdrant only as a ranked identity/score index. The only payload field
// consumed is asset_id; all returned metadata is loaded from SQLite.
// Result order and scores remain those produced by Qdrant. Missing or
// deleted SQLite rows are omitted rather than reconstructed from stale
// Qdrant payload data.
func (a *SearchAdapter) hydrateSearchResults(ctx context.Context, results []schema.SearchResult) ([]appsearch.VectorSearchResult, error) {
	if len(results) == 0 {
		return []appsearch.VectorSearchResult{}, nil
	}
	if a.assetStore == nil {
		return nil, fmt.Errorf("qdrant search hydration: SQLite asset store not configured")
	}

	out := make([]appsearch.VectorSearchResult, 0, len(results))
	for _, hit := range results {
		assetID := payloadString(hit.Payload, "asset_id")
		if assetID == "" {
			// A Qdrant hit without the identity projection cannot be
			// hydrated safely and must never be returned using its stale
			// metadata payload.
			continue
		}
		asset, err := a.assetStore.FetchAsset(ctx, assetID)
		if err != nil {
			if errors.Is(err, indexing.ErrAssetNotFound) {
				// Qdrant can briefly retain a point after the
				// canonical SQLite row is deleted. It is stale
				// projection data, never an API result.
				continue
			}
			return nil, fmt.Errorf("hydrate asset %q from SQLite: %w", assetID, err)
		}
		if asset == nil || asset.ID != assetID || asset.DeletedAt != "" {
			// SQLite is authoritative for existence and lifecycle. A
			// missing/tombstoned row is not a valid API result.
			continue
		}
		out = append(out, assetToVectorSearchResult(asset, hit))
	}
	return out, nil
}

func assetToVectorSearchResult(asset *indexing.AssetData, hit schema.SearchResult) appsearch.VectorSearchResult {
	return appsearch.VectorSearchResult{
		AssetID:        asset.ID,
		QdrantPointID:  hit.ID,
		Score:          hit.Score,
		Source:         asset.Source,
		Name:           asset.Name,
		Category:       asset.Category,
		MediaType:      asset.MediaType,
		Style:          asset.Style,
		Language:       asset.Language,
		YouTubeVideoID: asset.YouTubeVideoID,
		YouTubeURL:     asset.YouTubeURL,
		StartTime:      asset.StartTime,
		EndTime:        asset.EndTime,
		Tags:           append([]string(nil), asset.Tags...),
		SearchText:     asset.SearchText,
	}
}

// searchResultToVectorSearchResult is intentionally limited to the
// identity/score projection for callers that need the raw boundary in
// tests or diagnostics. It does not copy arbitrary Qdrant metadata.
func searchResultToVectorSearchResult(r schema.SearchResult) appsearch.VectorSearchResult {
	return appsearch.VectorSearchResult{
		AssetID:       payloadString(r.Payload, "asset_id"),
		QdrantPointID: r.ID,
		Score:         r.Score,
	}
}

// convertSearchResults is retained as a raw ID/score-only compatibility
// helper. API-facing paths must call hydrateSearchResults instead.
func convertSearchResults(results []schema.SearchResult) []appsearch.VectorSearchResult {
	out := make([]appsearch.VectorSearchResult, 0, len(results))
	for _, r := range results {
		out = append(out, searchResultToVectorSearchResult(r))
	}
	return out
}

// payloadString extracts a string value from a Qdrant payload map.
func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	raw, ok := payload[key]
	if !ok {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}
