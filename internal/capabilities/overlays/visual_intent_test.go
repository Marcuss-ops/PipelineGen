package overlays

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestVisualIntentResolver_MapsSemanticTypeVocabulary pins the frozen
// SemanticType → (kind, family, priority) mapping so a rename or a typo in the
// vocabulary never goes unnoticed.
func TestVisualIntentResolver_MapsSemanticTypeVocabulary(t *testing.T) {
	cases := []struct {
		semanticType string
		wantKind     VisualIntentKind
		wantFamily   PresetFamily
		wantPriority int
	}{
		{"PERSON", IntentKindEntityImage, FamilyPersonImage, 80},
		{"ORGANIZATION", IntentKindOrganizationCard, FamilyOrganization, 50},
		{"LOCATION", IntentKindLocationCard, FamilyLocation, 55},
		{"DATE", IntentKindDateBadge, FamilyDate, 60},
		{"MONEY", IntentKindImportantNumber, FamilyMoney, 90},
		{"NUMBER", IntentKindImportantNumber, FamilyNumber, 70},
		{"PERCENTAGE", IntentKindImportantNumber, FamilyPercentage, 88},
		{"IMPORTANT_PHRASE", IntentKindImportantText, FamilyImportantPhrase, 100},
		{"QUOTE", IntentKindQuoteCard, FamilyQuote, 75},
		{"CLAIM", IntentKindImportantText, FamilyClaim, 72},
		{"STATISTIC", IntentKindImportantNumber, FamilyStatistic, 85},
		{"RANKING", IntentKindRankingBadge, FamilyRanking, 75},
		{"TITLE", IntentKindTitleCard, FamilyTitle, 70},
		{"EVENT", IntentKindEventBadge, FamilyEvent, 50},
		{"IMAGE_ENTITY", IntentKindEntityImage, FamilyImageEntity, 60},
	}
	for _, tc := range cases {
		t.Run(tc.semanticType, func(t *testing.T) {
			got, ok := DefaultVisualIntentResolver.Resolve(VisualIntentInput{
				SemanticID: "sem_x",
				SceneID:    "scene_01",
				Type:       tc.semanticType,
				StartUS:    1_000_000,
				EndUS:      2_500_000,
			})
			require.True(t, ok)
			require.Equal(t, tc.wantKind, got.Kind)
			require.Equal(t, tc.wantFamily, got.PresetFamily)
			require.Equal(t, tc.wantPriority, got.Priority)
		})
	}
}

func TestVisualIntentResolver_ComputesTimingAndID(t *testing.T) {
	got, ok := DefaultVisualIntentResolver.Resolve(VisualIntentInput{
		SemanticID: "sem_scene03_person_01",
		SceneID:    "scene_03",
		Type:       "PERSON",
		StartUS:    12_400_000,
		EndUS:      13_900_000,
		AssetID:    "img_floyd_04",
	})
	require.True(t, ok)
	require.Equal(t, "intent-scene_03-sem_scene03_person_01", got.IntentID)
	require.Equal(t, "sem_scene03_person_01", got.SemanticID)
	require.Equal(t, "scene_03", got.SceneID)
	require.Equal(t, int64(12_400_000), got.StartUS)
	require.Equal(t, int64(1_500_000), got.DurationUS)
	require.Equal(t, "img_floyd_04", got.AssetID)
}

func TestVisualIntentResolver_UnknownTypeFailsClosed(t *testing.T) {
	_, ok := DefaultVisualIntentResolver.Resolve(VisualIntentInput{
		SemanticID: "sem_x",
		SceneID:    "scene_01",
		Type:       "SOMETHING_UNKNOWN",
		StartUS:    0,
		EndUS:      1_000_000,
	})
	require.False(t, ok)
}

func TestVisualIntentResolver_TypeNormalization(t *testing.T) {
	// The resolver normalizes the type (trim + upper) so the caller is free
	// of formatting constraints.
	got, ok := DefaultVisualIntentResolver.Resolve(VisualIntentInput{
		SemanticID: "sem_x",
		SceneID:    "scene_01",
		Type:       "  person ",
		StartUS:    0,
		EndUS:      1_000_000,
	})
	require.True(t, ok)
	require.Equal(t, IntentKindEntityImage, got.Kind)
	require.Equal(t, FamilyPersonImage, got.PresetFamily)
}

func TestVisualIntentResolver_Deterministic(t *testing.T) {
	in := VisualIntentInput{
		SemanticID: "sem_scene03_person_01",
		SceneID:    "scene_03",
		Type:       "PERSON",
		StartUS:    12_400_000,
		EndUS:      13_900_000,
	}
	first, ok := DefaultVisualIntentResolver.Resolve(in)
	require.True(t, ok)
	for i := 0; i < 100; i++ {
		got, _ := DefaultVisualIntentResolver.Resolve(in)
		require.Equal(t, first, got)
	}
}
