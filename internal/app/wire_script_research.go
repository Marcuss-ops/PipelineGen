package app

import (
	"context"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
)

type ollamaWebSearcherAdapter struct{ searcher *ollamaclient.WebSearcher }

func (a ollamaWebSearcherAdapter) Search(ctx context.Context, query string, limit int) ([]scriptports.WebSearchHit, error) {
	results, err := a.searcher.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	out := make([]scriptports.WebSearchHit, 0, len(results))
	for _, result := range results {
		out = append(out, scriptports.WebSearchHit{Title: result.Title, URL: result.URL, Content: result.Content})
	}
	return out, nil
}
