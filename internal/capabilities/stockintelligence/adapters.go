package stockintelligence

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/search"
)

type QdrantLocalSearchAdapter struct {
	Searcher   *search.Searcher
	Embedder   search.TextEmbedder
	VectorName string
}

func (a QdrantLocalSearchAdapter) SearchLocal(ctx context.Context, query string, limit int) ([]Candidate, error) {
	if a.Searcher == nil || a.Embedder == nil {
		return nil, fmt.Errorf("stockintelligence: local search not configured")
	}
	if limit <= 0 {
		limit = 20
	}
	hits, err := a.Searcher.SearchByText(ctx, query, a.Embedder, a.VectorName, limit, 0)
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(hits))
	for _, hit := range hits {
		out = append(out, Candidate{AssetID: hit.AssetID, GenericSimilarity: float32(hit.Score), Source: "local"})
	}
	return out, nil
}
