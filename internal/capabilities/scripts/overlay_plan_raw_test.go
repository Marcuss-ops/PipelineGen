package scriptgeneration

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
	"github.com/stretchr/testify/require"
)

func recipeCandidate(sceneID, recipe string, priority float64, rank int, payload string) SemanticProductionCandidate {
	return SemanticProductionCandidate{
		SceneID:       sceneID,
		Recipe:        recipe,
		Priority:      priority,
		Rank:          rank,
		VisualPayload: json.RawMessage(payload),
	}
}

func TestVisualRecipesForSceneForwardsEverySupportedRecipeInPriorityOrder(t *testing.T) {
	result := &GenerateResult{SemanticConsolidation: &SemanticVisualConsolidation{
		ProductionCandidates: []SemanticProductionCandidate{
			recipeCandidate("s1", "relationship_graph", 0.99, 1, `{"recipe":"relationship_graph"}`),
			recipeCandidate("s1", "counter_odometer", 0.8, 3, `{"recipe":"counter_odometer","value":12,"subject_label":"Debito"}`),
			recipeCandidate("s1", "role_lower_third", 0.7, 4, `{"recipe":"role_lower_third"}`),
			recipeCandidate("s2", "counter_odometer", 1, 2, `{"recipe":"counter_odometer","value":7}`),
		},
	}}

	recipes := visualRecipesForScene(result, "s1")
	require.Len(t, recipes, 2, "the unsupported graph is not shipped")
	require.Equal(t, "counter_odometer", recipes[0].Recipe, "priority decides the first element")
	require.JSONEq(t, `{"recipe":"counter_odometer","value":12,"subject_label":"Debito"}`, string(recipes[0].Payload))
	require.Equal(t, "role_lower_third", recipes[1].Recipe)
}

func TestVisualRecipesForSceneSkipsUnsupportedAndMalformedPayloads(t *testing.T) {
	result := &GenerateResult{SemanticConsolidation: &SemanticVisualConsolidation{
		ProductionCandidates: []SemanticProductionCandidate{
			recipeCandidate("s1", "counter_odometer", 0.9, 1, `not-json`),
			recipeCandidate("s1", "role_lower_third", 0.8, 2, `[]`),
			recipeCandidate("s1", "route_map", 0.7, 3, ``),
		},
	}}

	require.Empty(t, visualRecipesForScene(result, "s1"))
}

func TestVisualRecipesForSceneIsEmptyWithoutARecipe(t *testing.T) {
	require.Empty(t, visualRecipesForScene(nil, "s1"))
	require.Empty(t, visualRecipesForScene(&GenerateResult{}, "s1"))
}

func TestRawSceneCarriesEveryRecipeOnTheWire(t *testing.T) {
	raw := overlayplan.RawScene{
		SceneID:       "s1",
		VisualRecipe:  "counter_odometer",
		VisualPayload: json.RawMessage(`{"recipe":"counter_odometer","value":12}`),
		VisualRecipes: []overlayplan.VisualRecipeSpec{
			{Recipe: "counter_odometer", Payload: json.RawMessage(`{"recipe":"counter_odometer","value":12}`)},
			{Recipe: "role_lower_third", Payload: json.RawMessage(`{"recipe":"role_lower_third","person_label":"Tim Cook"}`)},
		},
	}
	encoded, err := json.Marshal(raw)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, "counter_odometer", decoded["visual_recipe"])
	payload, ok := decoded["visual_payload"].(map[string]any)
	require.True(t, ok, "payload must be a JSON object on the wire: %s", encoded)
	require.Equal(t, float64(12), payload["value"])
	list, ok := decoded["visual_recipes"].([]any)
	require.True(t, ok, "recipe list must be on the wire: %s", encoded)
	require.Len(t, list, 2)
	second := list[1].(map[string]any)
	require.Equal(t, "role_lower_third", second["recipe"])
	require.Equal(t, "Tim Cook", second["payload"].(map[string]any)["person_label"])
}

func TestVisualRecipesForSceneDropsTheSameFactTwice(t *testing.T) {
	result := &GenerateResult{SemanticConsolidation: &SemanticVisualConsolidation{
		ProductionCandidates: []SemanticProductionCandidate{
			recipeCandidate("s1", "quote_attribution", 0.9, 1, `{"recipe":"quote_attribution","author_label":"Mario Rossi"}`),
			recipeCandidate("s1", "quote_attribution", 0.9, 2, `{"recipe":"quote_attribution","author_label":"Mario Rossi"}`),
			recipeCandidate("s1", "quote_attribution", 0.8, 3, `{"recipe":"quote_attribution","author_label":"Anna Bianchi"}`),
		},
	}}

	recipes := visualRecipesForScene(result, "s1")
	require.Len(t, recipes, 2, "the identical pair travels once; two quotes still do")
	require.Contains(t, string(recipes[0].Payload), "Mario Rossi")
	require.Contains(t, string(recipes[1].Payload), "Anna Bianchi")
}
