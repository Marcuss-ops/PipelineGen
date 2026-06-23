// Package qdrant provides a canonical interface and Qdrant implementation for vector search.
package qdrant

import (
	"context"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
)

// SearchAdapter wraps a *Service and exposes it as an application-level
// search.VectorStorePort. It converts between qdrant infrastructure types
// and search application types structurally (they have identical field sets
// and JSON tags). This keeps the application layer free of qdrant imports.
type SearchAdapter struct {
	svc *Service
}

// NewSearchAdapter creates a SearchAdapter from a concrete *Service.
func NewSearchAdapter(svc *Service) appsearch.VectorStorePort {
	if svc == nil {
		return nil
	}
	return &SearchAdapter{svc: svc}
}

// Search delegates to the underlying Qdrant Service and converts types.
func (a *SearchAdapter) Search(ctx context.Context, req appsearch.VectorSearchRequest) ([]appsearch.VectorSearchResult, error) {
	qdrantReq := SearchRequest{
		QueryVector: req.QueryVector,
		VectorName:  req.VectorName,
		Limit:       req.Limit,
		MinScore:    req.MinScore,
		Source:      req.Source,
		Category:    req.Category,
		MediaType:   req.MediaType,
		Language:    req.Language,
	}
	results, err := a.svc.Search(ctx, qdrantReq)
	if err != nil {
		return nil, err
	}
	out := make([]appsearch.VectorSearchResult, len(results))
	for i, r := range results {
		out[i] = appsearch.VectorSearchResult{
			AssetID:        r.AssetID,
			QdrantPointID:  r.QdrantPointID,
			Score:          r.Score,
			Reason:         r.Reason,
			Source:         r.Source,
			Name:           r.Name,
			LocalPath:      r.LocalPath,
			DriveLink:      r.DriveLink,
			Category:       r.Category,
			MediaType:      r.MediaType,
			Style:          r.Style,
			Language:       r.Language,
			YouTubeVideoID: r.YouTubeVideoID,
			YouTubeURL:     r.YouTubeURL,
			StartTime:      r.StartTime,
			EndTime:        r.EndTime,
			Tags:           r.Tags,
			SearchText:     r.SearchText,
		}
	}
	return out, nil
}

// HybridSearch delegates to the underlying Qdrant Service and converts types.
func (a *SearchAdapter) HybridSearch(ctx context.Context, req appsearch.HybridSearchRequest) ([]appsearch.VectorSearchResult, error) {
	qdrantReq := HybridSearchRequest{
		QueryText:            req.QueryText,
		DenseVector:          req.DenseVector,
		DenseVectorName:      req.DenseVectorName,
		TranscriptVector:     req.TranscriptVector,
		TranscriptVectorName: req.TranscriptVectorName,
		SparseVectorName:     req.SparseVectorName,
		Limit:                req.Limit,
		MinScore:             req.MinScore,
		Source:               req.Source,
		Category:             req.Category,
		MediaType:            req.MediaType,
		Language:             req.Language,
	}
	results, err := a.svc.HybridSearch(ctx, qdrantReq)
	if err != nil {
		return nil, err
	}
	out := make([]appsearch.VectorSearchResult, len(results))
	for i, r := range results {
		out[i] = appsearch.VectorSearchResult{
			AssetID:        r.AssetID,
			QdrantPointID:  r.QdrantPointID,
			Score:          r.Score,
			Reason:         r.Reason,
			Source:         r.Source,
			Name:           r.Name,
			LocalPath:      r.LocalPath,
			DriveLink:      r.DriveLink,
			Category:       r.Category,
			MediaType:      r.MediaType,
			Style:          r.Style,
			Language:       r.Language,
			YouTubeVideoID: r.YouTubeVideoID,
			YouTubeURL:     r.YouTubeURL,
			StartTime:      r.StartTime,
			EndTime:        r.EndTime,
			Tags:           r.Tags,
			SearchText:     r.SearchText,
		}
	}
	return out, nil
}
