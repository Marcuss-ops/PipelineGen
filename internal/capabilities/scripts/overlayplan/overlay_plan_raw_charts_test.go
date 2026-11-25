package overlayplan

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/stretchr/testify/require"
)

func TestProductionChartValuesForSceneCarriesRankingAndDeltaCandidates(t *testing.T) {
	result := &contract.GenerateResult{SemanticConsolidation: &contract.SemanticVisualConsolidation{
		ProductionCandidates: []contract.SemanticProductionCandidate{
			{SceneID: "s1", Recipe: "ranking_bar", ChartItems: []contract.SemanticChartItem{{Text: "10 EUR"}, {Text: "9 EUR"}}},
			{SceneID: "s1", Recipe: "delta_comparison", ChartItems: []contract.SemanticChartItem{{Text: "10%"}, {Text: "10 EUR"}}},
			{SceneID: "s1", Recipe: "chart", ChartItems: []contract.SemanticChartItem{{Text: "ignored"}}},
			{SceneID: "s2", Recipe: "ranking_bar", ChartItems: []contract.SemanticChartItem{{Text: "other-scene"}}},
		},
	}}

	values := mergeProductionChartValues([]string{"existing"}, productionChartValuesForScene(result, "s1"))
	require.Equal(t, []string{"existing", "10 EUR", "9 EUR", "10%"}, values)
	require.Empty(t, productionChartValuesForScene(result, "s3"))
}
