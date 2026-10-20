package overlays

import (
	"testing"

	capanimreg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/animreg"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
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
		{"PRODUCT", IntentKindEntityImage, FamilyProduct, 65},
		{"TIME", IntentKindImportantNumber, FamilyTime, 58},
		{"CHAPTER", IntentKindChapterCard, FamilyChapterCard, 68},
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

func TestVisualIntentResolver_NewVocabularyEndToEnd(t *testing.T) {
	// PRODUCT resolves to an image intent carrying an optional asset binding;
	// TIME resolves like the other numeric value artifacts; CHAPTER resolves
	// to the full-frame chapter-card kind whose overlay kind owns a
	// chapter_card motion group in the registry (never an empty overlay).
	product, ok := DefaultVisualIntentResolver.Resolve(VisualIntentInput{
		SemanticID: "sem_scene02_product_01",
		SceneID:    "scene_02",
		Type:       "PRODUCT", // entities.SemanticProduct — literal to avoid the entities→overlays import cycle
		StartUS:    4_000_000,
		EndUS:      6_500_000,
		AssetID:    "img_iphone17_01",
	})
	require.True(t, ok)
	require.Equal(t, IntentKindEntityImage, product.Kind)
	require.Equal(t, FamilyProduct, product.PresetFamily)
	require.Equal(t, "img_iphone17_01", product.AssetID)

	time, ok := DefaultVisualIntentResolver.Resolve(VisualIntentInput{
		SemanticID: "sem_scene04_time_01",
		SceneID:    "scene_04",
		Type:       "TIME", // entities.SemanticTime
		StartUS:    7_000_000,
		EndUS:      8_000_000,
	})
	require.True(t, ok)
	require.Equal(t, IntentKindImportantNumber, time.Kind)
	require.Equal(t, FamilyTime, time.PresetFamily)

	chapter, ok := DefaultVisualIntentResolver.Resolve(VisualIntentInput{
		SemanticID: "sem_scene06_chapter_01",
		SceneID:    "scene_06",
		Type:       "CHAPTER", // entities.SemanticChapter
		StartUS:    0,
		EndUS:      3_000_000,
	})
	require.True(t, ok)
	require.Equal(t, IntentKindChapterCard, chapter.Kind)
	require.Equal(t, FamilyChapterCard, chapter.PresetFamily)
	// The registry must author at least one selectable motion for the
	// chapter_card overlay kind, so a chapter intent never plans empty.
	groups := capanimreg.ItemMotionGroups(string(capmodel.KindChapterCard))
	require.NotEmpty(t, groups)
	for _, group := range groups {
		require.NotEmpty(t, capanimreg.AnimationCandidates(group),
			"chapter_card motion group %q must resolve to animations", group)
	}
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
