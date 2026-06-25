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
	"github.com/Marcuss-ops/PipelineGen/pkg/bm25"
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
func (a *searchAdapter) Search(ctx context.Context, req appsearch.VectorSearchRequest) ([]appsearch.VectorSearchResult, error) {
	if a.searcher == nil {
		return nil, fmt.Errorf("qdrant searcher not configured")
	}

	// Build the qdrant-level filter from application filter fields.
	var filter map[string]interface{}
	if req.Source != "" || req.Category != "" || req.MediaType != "" || req.Language != "" {
		must := make([]map[string]interface{}, 0, 4)
		if req.Source != "" {
			must = append(must, map[string]interface{}{
				"key": "source", "match": map[string]interface{}{"value": req.Source},
			})
		}
		if req.Category != "" {
			must = append(must, map[string]interface{}{
				"key": "category", "match": map[string]interface{}{"value": req.Category},
			})
		}
		if req.MediaType != "" {
			must = append(must, map[string]interface{}{
				"key": "media_type", "match": map[string]interface{}{"value": req.MediaType},
			})
		}
		if req.Language != "" {
			must = append(must, map[string]interface{}{
				"key": "language", "match": map[string]interface{}{"value": req.Language},
			})
		}
		filter = map[string]interface{}{"must": must}
	}

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
func (a *searchAdapter) HybridSearch(ctx context.Context, req appsearch.HybridSearchRequest) ([]appsearch.VectorSearchResult, error) {
	if a.searcher == nil {
		return nil, fmt.Errorf("qdrant searcher not configured")
	}

	// Build filter.
	var filter map[string]interface{}
	if req.Source != "" || req.Category != "" || req.MediaType != "" || req.Language != "" {
		must := make([]map[string]interface{}, 0, 4)
		if req.Source != "" {
			must = append(must, map[string]interface{}{
				"key": "source", "match": map[string]interface{}{"value": req.Source},
			})
		}
		if req.Category != "" {
			must = append(must, map[string]interface{}{
				"key": "category", "match": map[string]interface{}{"value": req.Category},
			})
		}
		if req.MediaType != "" {
			must = append(must, map[string]interface{}{
				"key": "media_type", "match": map[string]interface{}{"value": req.MediaType},
			})
		}
		if req.Language != "" {
			must = append(must, map[string]interface{}{
				"key": "language", "match": map[string]interface{}{"value": req.Language},
			})
		}
		filter = map[string]interface{}{"must": must}
	}

	// QDRANT-004: tokenize query text into BM25 sparse vector
	// for real hybrid (dense + sparse) retrieval via RRF fusion.
	var sparseVec *SparseQueryVector
	if req.SparseVectorName != "" && req.QueryText != "" {
		if sv := bm25.Tokenize(req.QueryText); sv != nil {
			sparseVec = &SparseQueryVector{
				Indices: sv.Indices,
				Values:  sv.Values,
			}
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
	sr.LocalPath = payloadString(r.Payload, "local_path")
	sr.DriveLink = payloadString(r.Payload, "drive_link")
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
