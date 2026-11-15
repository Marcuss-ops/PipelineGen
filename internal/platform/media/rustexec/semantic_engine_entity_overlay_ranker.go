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
	if len(response.SelectedIndices) != len(scenes) {
		return nil, fmt.Errorf("semantic-engine entity overlay ranker returned %d scene selections for %d scenes", len(response.SelectedIndices), len(scenes))
	}
	for sceneIndex, scene := range scenes {
		seenIndices := make(map[int]struct{}, len(response.SelectedIndices[sceneIndex]))
		seenOverlayIDs := make(map[string]struct{}, len(response.SelectedIndices[sceneIndex]))
		for _, index := range response.SelectedIndices[sceneIndex] {
			if index < 0 || index >= len(scene.Entities) {
				return nil, fmt.Errorf("semantic-engine entity overlay ranker returned index %d outside scene %d with %d entities", index, sceneIndex, len(scene.Entities))
			}
			if _, duplicate := seenIndices[index]; duplicate {
				return nil, fmt.Errorf("semantic-engine entity overlay ranker repeated index %d in scene %d", index, sceneIndex)
			}
			seenIndices[index] = struct{}{}
			id := scene.Entities[index].OverlayID
			if _, duplicate := seenOverlayIDs[id]; duplicate {
				return nil, fmt.Errorf("semantic-engine entity overlay ranker repeated overlay id %q in scene %d", id, sceneIndex)
			}
			seenOverlayIDs[id] = struct{}{}
		}
		expected := make(map[string]struct{}, len(scene.Entities))
		for _, entity := range scene.Entities {
			expected[entity.OverlayID] = struct{}{}
		}
		if len(seenOverlayIDs) != len(expected) {
			return nil, fmt.Errorf("semantic-engine entity overlay ranker selected %d unique overlays for %d identities in scene %d", len(seenOverlayIDs), len(expected), sceneIndex)
		}
	}
	return response.SelectedIndices, nil
}

var _ capabilityentities.EntityOverlayRanker = (*SemanticEngineAdapter)(nil)
