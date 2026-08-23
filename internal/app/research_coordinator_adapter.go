package app

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/research"
)

// researchCoordinatorAdapter is composition glue. The research capability
// must not import the legacy scripts usecase merely to satisfy its port.
type researchCoordinatorAdapter struct {
	coordinator *research.ResearchSearchCoordinator
}

func (a *researchCoordinatorAdapter) SearchWithFallback(ctx context.Context, subject string, queries []string, targetPool int) []usecase.CoordinatorSearchResult {
	results := a.coordinator.SearchWithFallback(ctx, subject, queries, targetPool)
	out := make([]usecase.CoordinatorSearchResult, len(results))
	for i, result := range results {
		out[i] = usecase.CoordinatorSearchResult{
			Hit:        result.Hit,
			Provider:   result.Provider,
			QueryLevel: result.QueryLevel,
		}
	}
	return out
}
