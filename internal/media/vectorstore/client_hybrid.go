package vectorstore

import (
	"context"
	"fmt"
)

// HybridSearch performs hybrid dense+sparse search using Qdrant prefetch + RRF fusion.
// Supports up to 3-way fusion:
//  1. Semantic vector ("text") — general meaning (title + summary + topics + hook)
//  2. Transcript vector ("transcript") — precise speech content (new!)
//  3. Sparse BM25 vector ("bm25_text") — keyword matching
//
// Each vector that is provided gets a prefetch query; results are merged via RRF.
func (c *QdrantClient) HybridSearch(ctx context.Context, req HybridSearchRequest) ([]SearchResult, error) {
	if req.Limit <= 0 {
		req.Limit = 10
	}

	denseName := req.DenseVectorName
	if denseName == "" {
		denseName = c.cfg.TextVectorName
	}
	transcriptName := req.TranscriptVectorName
	if transcriptName == "" && c.cfg.TranscriptVectorName != "" {
		transcriptName = c.cfg.TranscriptVectorName
	}
	sparseName := req.SparseVectorName
	if sparseName == "" {
		sparseName = c.cfg.SparseVectorName
	}

	// Defensive cap: prefetchLimit is req.Limit * 10 to give RRF enough
	// candidates to fuse, but if a future caller bypasses ParsePagination
	// (e.g. a new internal entry point that doesn't go through the
	// handler middleware) req.Limit could be huge. We check req.Limit
	// itself BEFORE the multiplication to avoid int-overflow wraparound
	// (Go's int * int wraps silently on overflow, which would defeat
	// the clamp). Once req.Limit is bounded to 100, the multiplication
	// is guaranteed safe. 1000 = 100x the typical req.Limit and plenty
	// of headroom for RRF fusion. The final `limit: req.Limit` in the
	// request body still trims to the user-requested count, so this
	// only affects how many candidates RRF can rank.
	prefetchLimit := 1000
	if req.Limit <= 100 {
		prefetchLimit = req.Limit * 10
	}

	// Build prefetch: semantic (text) + transcript + sparse BM25
	prefetch := []map[string]any{}

	if len(req.DenseVector) > 0 {
		prefetch = append(prefetch, map[string]any{
			"query": req.DenseVector,
			"using": denseName,
			"limit": prefetchLimit,
		})
	}

	if len(req.TranscriptVector) > 0 && transcriptName != "" {
		prefetch = append(prefetch, map[string]any{
			"query": req.TranscriptVector,
			"using": transcriptName,
			"limit": prefetchLimit,
		})
	}

	if req.SparseVector != nil && len(req.SparseVector.Indices) > 0 && sparseName != "" {
		prefetch = append(prefetch, map[string]any{
			"query": map[string]any{
				"indices": req.SparseVector.Indices,
				"values":  req.SparseVector.Values,
			},
			"using": sparseName,
			"limit": prefetchLimit,
		})
	}

	if len(prefetch) == 0 {
		return nil, fmt.Errorf("no vectors provided for hybrid search")
	}

	searchReq := map[string]any{
		"prefetch":     prefetch,
		"query":        map[string]any{"fusion": "rrf"},
		"limit":        req.Limit,
		"with_payload": true,
	}

	// Add optional filters
	var mustConditions []map[string]any
	if req.Source != "" {
		mustConditions = append(mustConditions, map[string]any{
			"key": "source", "match": map[string]any{"value": req.Source},
		})
	}
	if req.Category != "" {
		mustConditions = append(mustConditions, map[string]any{
			"key": "category", "match": map[string]any{"value": req.Category},
		})
	}
	if req.MediaType != "" {
		mustConditions = append(mustConditions, map[string]any{
			"key": "media_type", "match": map[string]any{"value": req.MediaType},
		})
	}
	if req.Language != "" {
		mustConditions = append(mustConditions, map[string]any{
			"key": "language", "match": map[string]any{"value": req.Language},
		})
	}

	if len(mustConditions) > 0 {
		searchReq["filter"] = map[string]any{
			"must": mustConditions,
		}
	}

	// Use /points/query endpoint (Qdrant >= 1.7) for prefetch + RRF fusion.
	// /points/search does NOT support prefetch — it requires a vector field.
	respBody, err := c.qdrantRequest(ctx, "POST",
		fmt.Sprintf("/collections/%s/points/query", c.operationCollection()), searchReq)
	if err != nil {
		return nil, fmt.Errorf("hybrid search: %w", err)
	}

	// RRF fusion scores are rank-based (typically < 0.5), NOT cosine similarity.
	// Do NOT default to MinInstantScore (cosine threshold) for hybrid search.
	minScore := req.MinScore

	return parseQueryResults(respBody, minScore, req.Limit)
}
