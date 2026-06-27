// Package qdrant — SearchAdapter bridges the infrastructure-level qdrant.Searcher
// to the application-level search.VectorStorePort interface. Per AGENTS.md Pattern 0
// (Port abstraction layer), this adapter is the ONLY place that imports both
// qdrant types and application-level search types.
//
// QDRANT-003: The adapter converts application-layer request/response DTOs
// (VectorSearchRequest, HybridSearchRequest, VectorSearchResult) into the
// canonical qdrant types (SearchRequest, HybridSearchRequest, SearchResult)
// and back.
package qdrant

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
)

// searchAdapter adapts qdrant.Searcher to search.VectorStorePort.
type searchAdapter struct {
	searcher *Searcher
	log      *zap.Logger
}

// NewSearchAdapter creates a search.VectorStorePort implementation backed
// by the Qdrant Searcher. The caller is responsible for wiring the adapter
// into the application layer (e.g. search.Service's vectorStore field).
func NewSearchAdapter(searcher *Searcher, log *zap.Logger) appsearch.VectorStorePort {
	return &searchAdapter{searcher: searcher, log: log}
}

// Compile-time assertion.
var _ appsearch.VectorStorePort = (*searchAdapter)(nil)

// Search converts an application-level VectorSearchRequest into a qdrant
// SearchRequest, delegates to the Searcher, and converts results back.
//
// PR 1 (June 2026, Lifecycle state SSOT): the canonical lifecycle_state
// filter is {\"ACTIVE\"} only. Pre-PR1 the waterfall was {\"active\",
// \"searchable\"} — both legacy values are pruned by migration 101 and
// no production code path writes a non-ACTIVE searchable value anymore.
func (a *searchAdapter) Search(ctx context.Context, req appsearch.VectorSearchRequest) ([]appsearch.VectorSearchResult, error) {
	if a.searcher == nil {
		return nil, fmt.Errorf("qdrant searcher not configured")
	}

	filter := buildLifecycleAwareFilter(req.Source, req.Category, req.MediaType, req.Language, req.WorkspaceID)

	qReq := SearchRequest{
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

// HybridSearch converts an application-level HybridSearchRequest into a
// qdrant HybridSearchRequest, delegates to the Searcher, and converts back.
//
// PR 1 (June 2026, Lifecycle state SSOT): same canonical ACTIVE-only
// filter as Search() so hybrid results never include DELETED/STAGING/
// PROCESSING/DELETE_PENDING/ERROR points.
func (a *searchAdapter) HybridSearch(ctx context.Context, req appsearch.HybridSearchRequest) ([]appsearch.VectorSearchResult, error) {
	if a.searcher == nil {
		return nil, fmt.Errorf("qdrant searcher not configured")
	}

	filter := buildLifecycleAwareFilter(req.Source, req.Category, req.MediaType, req.Language, req.WorkspaceID)

	// QDRANT-004 PR1 (June 2026): the orchestrator now owns BM25
	// tokenization (mediasearch.Service builds the *bm25.SparseVector
	// and sets HybridSearchRequest.SparseVector). The adapter becomes
	// a pure DTO → Qdrant mapper: project Fields{Indices, Values} into
	// the infrastructure-level SparseQueryVector. Any nil vector here
	// means the orchestrator failed to enforce fail-closed — the
	// downstream qdrant.Searcher.HybridSearch will reject with
	// ErrSparseRequired as defence-in-depth.
	var sparseVec *SparseQueryVector
	if req.SparseVector != nil {
		sparseVec = &SparseQueryVector{
			Indices: req.SparseVector.Indices,
			Values:  req.SparseVector.Values,
		}
	}

	qReq := HybridSearchRequest{
		DenseVector:       req.DenseVector,
		DenseVectorName:   req.DenseVectorName,
		SparseVectorName:  req.SparseVectorName,
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

// convertSearchResults maps qdrant.SearchResult → search.VectorSearchResult.
func convertSearchResults(results []SearchResult) []appsearch.VectorSearchResult {
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
// from both qdrant.SearchResult (infra) and appsearch.VectorSearchResult
// (application DTO). The search contract is now locator-free.
func searchResultToVectorSearchResult(r SearchResult) appsearch.VectorSearchResult {
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
		case []interface{}:
			sr.Tags = make([]string, len(v))
			for i, item := range v {
				sr.Tags[i] = fmt.Sprint(item)
			}
		}
	}

	return sr
}

// payloadString extracts a string value from a Qdrant payload map.
func payloadString(payload map[string]interface{}, key string) string {
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

// buildLifecycleAwareFilter is the canonical filter builder for Qdrant
// search + hybrid search (PR 1 — Lifecycle state SSOT, June 2026).
//
// The function ALWAYS appends the lifecycle_state must-clause even when
// all caller-side filters are empty — a vector query without a
// lifecycle filter could return TOMBSTONE rows (DELETE_PENDING/DELETED/
// ERROR/STAGING/PROCESSING) to the application layer, which would be a
// "no fake availability" invariant violation. Defence-in-depth: the
// post-query guard in mediasearch/service.go drops rows whose
// lifecycle_state is not in SearchableLifecycleStates, but enforcing
// the constraint at the Qdrant boundary keeps the user-visible surface
// (SearchHit) immune to a future orchestrator that forgets the
// defence-in-depth layer.
//
// The canonical value is "ACTIVE". Pre-PR1 the filter was a 2-value
// waterfall {"active", "searchable"}; both legacy values were pruned
// by migration 101 so a single-string match is enough. Hybrid and
// pure ANN share this builder — the no-filter-vs-with-filter split is
// gone so a Searcher wiring that sets QueryVector but forgets a
// Filter arg can no longer silently bypass the lifecycle clause.
//
// Parameters mirror the previous inline builder exactly so the wire
// shape of the Qdrant filter is byte-identical between callers (the
// only difference from the pre-PR1 inline builder is the lifecycle
// filter value-list).
func buildLifecycleAwareFilter(source, category, mediaType, language, workspaceID string) map[string]interface{} {
	must := make([]map[string]interface{}, 0, 6)
	if source != "" {
		must = append(must, map[string]interface{}{
			"key": "source", "match": map[string]interface{}{"value": source},
		})
	}
	if category != "" {
		must = append(must, map[string]interface{}{
			"key": "category", "match": map[string]interface{}{"value": category},
		})
	}
	if mediaType != "" {
		must = append(must, map[string]interface{}{
			"key": "media_type", "match": map[string]interface{}{"value": mediaType},
		})
	}
	if language != "" {
		must = append(must, map[string]interface{}{
			"key": "language", "match": map[string]interface{}{"value": language},
		})
	}
	// QDRANT-004 §workspace_id isolation — vector/hybrid search must
	// only return points belonging to the caller's workspace.
	if workspaceID != "" {
		must = append(must, map[string]interface{}{
			"key": "workspace_id", "match": map[string]interface{}{"value": workspaceID},
		})
	}
	// Canonical lifecycle filter (PR 1, June 2026): single value
	// {\"ACTIVE\"} replaces the legacy {\"active\", \"searchable\"}
	// waterfall. The lifecycle_state payload key is SSOT (QDRANT-004
	// PR2); the previous \"status\" payload key was retired in this
	// same commit and any pre-PR1 point that wrote it is repaired
	// by the reconciler's KindLifecycleKeyLegacy classification.
	must = append(must, map[string]interface{}{
		"key":   "lifecycle_state",
		"match": map[string]interface{}{"value": string("ACTIVE")},
	})
	return map[string]interface{}{"must": must}
}
