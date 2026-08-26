package usecase

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/research"
)

// ResearchCoordinatorAdapter adapts the research capability's
// ResearchSearchCoordinator to the WebResearchResolver search-coordinator
// port. It lives next to the port so the scripts usecase never needs to know
// the research capability's concrete wiring; the composition root constructs
// it with the already-built coordinator.
type ResearchCoordinatorAdapter struct {
	coordinator *research.ResearchSearchCoordinator
}

// NewResearchCoordinatorAdapter builds the adapter around the canonical
// coordinator concrete.
func NewResearchCoordinatorAdapter(coordinator *research.ResearchSearchCoordinator) *ResearchCoordinatorAdapter {
	return &ResearchCoordinatorAdapter{coordinator: coordinator}
}

// SearchWithFallback implements researchSearchCoordinatorPort.
func (a *ResearchCoordinatorAdapter) SearchWithFallback(ctx context.Context, subject string, queries []string, targetPool int) []CoordinatorSearchResult {
	if a == nil || a.coordinator == nil {
		return nil
	}
	results := a.coordinator.SearchWithFallback(ctx, subject, queries, targetPool)
	out := make([]CoordinatorSearchResult, len(results))
	for i, result := range results {
		out[i] = CoordinatorSearchResult{
			Hit:        result.Hit,
			Provider:   result.Provider,
			QueryLevel: result.QueryLevel,
		}
	}
	return out
}
