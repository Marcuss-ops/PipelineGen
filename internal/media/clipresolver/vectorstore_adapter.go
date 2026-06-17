package clipresolver

import (
	"context"

	"velox/go-master/internal/media/vectorstore"
)

// VectorStoreAdapter wraps a vectorstore.Service to implement VectorStoreSearcher.
type VectorStoreAdapter struct {
	svc *vectorstore.Service
}

// NewVectorStoreAdapter creates an adapter from vectorstore.Service to VectorStoreSearcher.
func NewVectorStoreAdapter(svc *vectorstore.Service) *VectorStoreAdapter {
	return &VectorStoreAdapter{svc: svc}
}

// Search implements VectorStoreSearcher by delegating to the vector store.
func (a *VectorStoreAdapter) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if a.svc == nil || !a.svc.Enabled() {
		return nil, nil
	}

	results, err := a.svc.Search(ctx, vectorstore.SearchRequest{
		QueryVector: req.QueryVector,
		VectorName:  req.VectorName,
		Limit:       req.Limit,
		MinScore:    req.MinScore,
		Source:      req.Source,
		Category:    req.Category,
		MediaType:   req.MediaType,
	})
	if err != nil {
		return nil, err
	}

	out := make([]SearchResult, 0, len(results))
	for _, r := range results {
		out = append(out, SearchResult{
			AssetID:   r.AssetID,
			Score:     r.Score,
			Source:    r.Source,
			Name:      r.Name,
			LocalPath: r.LocalPath,
			DriveLink: r.DriveLink,
			Category:  r.Category,
			MediaType: r.MediaType,
		})
	}
	return out, nil
}
