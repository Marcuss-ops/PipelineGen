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
	"fmt"

	"go.uber.org/zap"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
)

// SearchAdapter adapts qdrant.Searcher to search.VectorStorePort.
type SearchAdapter struct {
	searcher *Searcher
	log      *zap.Logger
}

// NewSearchAdapter creates a search.VectorStorePort implementation backed
// by the Qdrant Searcher. The caller is responsible for wiring the adapter
// into the application layer (e.g. search.Service's vectorStore field).
func NewSearchAdapter(searcher *Searcher, log *zap.Logger) *SearchAdapter {
	return &SearchAdapter{searcher: searcher, log: log}
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

	return convertSearchResults(results), nil
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

	return convertSearchResults(results), nil
}

// ── Conversion helpers ─────────────────────────────────────────────────

// convertSearchResults maps qdrant.schema.SearchResult → search.VectorSearchResult.
func convertSearchResults(results []schema.SearchResult) []appsearch.VectorSearchResult {
	out := make([]appsearch.VectorSearchResult, 0, len(results))
	for _, r := range results {
		out = append(out, searchResultToVectorSearchResult(r))
	}
	return out
}

// searchResultToVectorSearchResult converts a single Qdrant search result
// to the application-level DTO, extracting known payload fields.
//
// QDRANT-001 (June 2026): LocalPath and DriveLink have been removed
// from both qdrant.schema.SearchResult (infra) and appsearch.VectorSearchResult
// (application DTO). The search contract is now locator-free.
func searchResultToVectorSearchResult(r schema.SearchResult) appsearch.VectorSearchResult {
	sr := appsearch.VectorSearchResult{
		QdrantPointID: r.ID,
		Score:         r.Score,
	}
	if r.Payload == nil {
		return sr
	}

	// Extract scalar fields.
	sr.AssetID = payloadString(r.Payload, "asset_id")
	sr.Source = payloadString(r.Payload, "source")
	sr.Name = payloadString(r.Payload, "name")
	sr.Category = payloadString(r.Payload, "category")
	sr.MediaType = payloadString(r.Payload, "media_type")
	sr.Style = payloadString(r.Payload, "style")
	sr.Language = payloadString(r.Payload, "language")
	sr.YouTubeVideoID = payloadString(r.Payload, "youtube_video_id")
	sr.YouTubeURL = payloadString(r.Payload, "youtube_url")
	sr.StartTime = payloadString(r.Payload, "start_time")
	sr.EndTime = payloadString(r.Payload, "end_time")
	sr.SearchText = payloadString(r.Payload, "search_text")

	// Extract tags ([]any → []string).
	if raw, ok := r.Payload["tags"]; ok {
		switch v := raw.(type) {
		case []string:
			sr.Tags = append([]string(nil), v...)
		case []any:
			sr.Tags = make([]string, len(v))
			for i, item := range v {
				sr.Tags[i] = fmt.Sprint(item)
			}
		}
	}

	return sr
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
