package rustexec

import (
	"context"
	"fmt"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
)

// RankEntityOverlays delegates score calculation, stable ordering and
// duplicate-card admission to BrainRust; Go keeps ownership of the full
// timeline and render DTOs.
func (a *SemanticEngineAdapter) RankEntityOverlays(ctx context.Context, scenes []capabilityentities.EntityOverlayRankingScene) ([][]int, error) {
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("semantic-engine entity overlay ranker: executor is not configured")
	}
	compilation, err := a.CompileEditorialPlan(ctx, nil, nil, scenes, nil, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine entity overlay ranker: %w", err)
	}
	return compilation.RankedEntities, nil
}

var _ capabilityentities.EntityOverlayRanker = (*SemanticEngineAdapter)(nil)
