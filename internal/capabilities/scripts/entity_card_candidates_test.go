package scriptgeneration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
)

func TestAllEntityCandidatesRankerSelectsEveryOccurrence(t *testing.T) {
	scenes := []capabilityentities.EntityOverlayRankingScene{
		{Entities: []capabilityentities.EntityOverlayRankingOccurrence{
			{EntityID: "person-tim", OverlayID: "overlay-s1-tim", EntityType: "PERSON", Confidence: .98},
			{EntityID: "person-tim", OverlayID: "overlay-s1-tim", EntityType: "PERSON", Confidence: .98},
			{EntityID: "org-apple", OverlayID: "overlay-s1-apple", EntityType: "ORG", Confidence: .9},
		}},
		{Entities: []capabilityentities.EntityOverlayRankingOccurrence{
			{EntityID: "person-tim", OverlayID: "overlay-s2-tim", EntityType: "PERSON", Confidence: .98},
		}},
	}
	selected, err := (allEntityCandidatesRanker{}).RankEntityOverlays(context.Background(), scenes)
	require.NoError(t, err)
	require.Equal(t, [][]int{{0, 1, 2}, {0}}, selected)
}
