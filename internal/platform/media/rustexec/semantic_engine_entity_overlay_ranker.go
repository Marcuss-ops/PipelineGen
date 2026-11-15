package rustexec

import (
	"context"
	"encoding/json"
	"fmt"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
)

type rankEntityOverlaysRequest struct {
	Operation string                                         `json:"operation"`
	Scenes    []capabilityentities.EntityOverlayRankingScene `json:"scenes"`
}

type rankEntityOverlaysResponse struct {
	OK              bool    `json:"ok"`
	Error           string  `json:"error,omitempty"`
	SelectedIndices [][]int `json:"selected_indices"`
}

// RankEntityOverlays delegates score calculation, stable ordering and
// duplicate-card admission to BrainRust; Go keeps ownership of the full
// timeline and render DTOs.
func (a *SemanticEngineAdapter) RankEntityOverlays(ctx context.Context, scenes []capabilityentities.EntityOverlayRankingScene) ([][]int, error) {
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("semantic-engine entity overlay ranker: executor is not configured")
	}
	request := rankEntityOverlaysRequest{Operation: "rank_entity_overlays", Scenes: scenes}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine entity overlay ranker encode: %w", err)
	}
	stdout, _, err := a.executor.Run(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine entity overlay ranker execute: %w", err)
	}
	var response rankEntityOverlaysResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, fmt.Errorf("semantic-engine entity overlay ranker decode: %w", err)
	}
	if !response.OK {
		return nil, fmt.Errorf("semantic-engine entity overlay ranker worker: %s", response.Error)
	}
	if err := validateEntitySelections(scenes, response.SelectedIndices, "semantic-engine entity overlay ranker"); err != nil {
		return nil, err
	}
	return response.SelectedIndices, nil
}

var _ capabilityentities.EntityOverlayRanker = (*SemanticEngineAdapter)(nil)
