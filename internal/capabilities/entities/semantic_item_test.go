package entities

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// validSemanticItem mirrors the canonical semantic-index example: a grounded
// PERSON item with both a rune span and a microsecond span.
func validSemanticItem() SemanticItem {
	return SemanticItem{
		SemanticID:     "sem_scene03_person_01",
		SceneID:        "scene_03",
		Type:           SemanticPerson,
		Text:           "Floyd Mayweather",
		NormalizedText: "floyd mayweather",
		StartChar:      153,
		EndChar:        170,
		StartUS:        12_400_000,
		EndUS:          13_900_000,
		Confidence:     0.98,
	}
}

func TestSemanticItem_ValidateOK(t *testing.T) {
	require.NoError(t, validSemanticItem().Validate())
}

func TestSemanticItem_ValidateWithCanonicalEntityID(t *testing.T) {
	item := validSemanticItem()
	item.CanonicalEntityID = "person:floyd-mayweather-jr"
	require.NoError(t, item.Validate())
}

func TestSemanticItem_ValidateRejectsEmptyIdentity(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*SemanticItem)
	}{
		{"empty semantic_id", func(i *SemanticItem) { i.SemanticID = "" }},
		{"blank semantic_id", func(i *SemanticItem) { i.SemanticID = "  " }},
		{"empty scene_id", func(i *SemanticItem) { i.SceneID = "" }},
		{"empty type", func(i *SemanticItem) { i.Type = "" }},
		{"empty text", func(i *SemanticItem) { i.Text = "" }},
		{"empty normalized_text", func(i *SemanticItem) { i.NormalizedText = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := validSemanticItem()
			tc.mutate(&item)
			require.ErrorIs(t, item.Validate(), ErrInvalidSemanticItem)
		})
	}
}

func TestSemanticItem_ValidateRejectsBadSpans(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*SemanticItem)
	}{
		{"inverted char span", func(i *SemanticItem) { i.StartChar, i.EndChar = 170, 153 }},
		{"zero-width char span", func(i *SemanticItem) { i.StartChar, i.EndChar = 153, 153 }},
		{"negative char start", func(i *SemanticItem) { i.StartChar = -1 }},
		{"inverted us span", func(i *SemanticItem) { i.StartUS, i.EndUS = 13_900_000, 12_400_000 }},
		{"zero-width us span", func(i *SemanticItem) { i.StartUS, i.EndUS = 12_400_000, 12_400_000 }},
		{"negative us start", func(i *SemanticItem) { i.StartUS = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := validSemanticItem()
			tc.mutate(&item)
			require.ErrorIs(t, item.Validate(), ErrInvalidSemanticItem)
		})
	}
}

func TestSemanticItem_ValidateRejectsBadConfidence(t *testing.T) {
	for _, confidence := range []float64{-0.1, 1.1} {
		item := validSemanticItem()
		item.Confidence = confidence
		require.ErrorIs(t, item.Validate(), ErrInvalidSemanticItem)
	}
}

// TestSemanticTypeVocabulary pins the canonical vocabulary so a rename or a
// typo never goes unnoticed: the semantic index is only as reliable as the
// type spelling every downstream resolver agrees on.
func TestSemanticTypeVocabulary(t *testing.T) {
	require.Equal(t, SemanticType("PERSON"), SemanticPerson)
	require.Equal(t, SemanticType("ORGANIZATION"), SemanticOrganization)
	require.Equal(t, SemanticType("LOCATION"), SemanticLocation)
	require.Equal(t, SemanticType("DATE"), SemanticDate)
	require.Equal(t, SemanticType("MONEY"), SemanticMoney)
	require.Equal(t, SemanticType("NUMBER"), SemanticNumber)
	require.Equal(t, SemanticType("PERCENTAGE"), SemanticPercentage)
	require.Equal(t, SemanticType("IMPORTANT_PHRASE"), SemanticImportantPhrase)
	require.Equal(t, SemanticType("QUOTE"), SemanticQuote)
	require.Equal(t, SemanticType("CLAIM"), SemanticClaim)
	require.Equal(t, SemanticType("STATISTIC"), SemanticStatistic)
	require.Equal(t, SemanticType("RANKING"), SemanticRanking)
	require.Equal(t, SemanticType("TITLE"), SemanticTitle)
	require.Equal(t, SemanticType("EVENT"), SemanticEvent)
	require.Equal(t, SemanticType("IMAGE_ENTITY"), SemanticImageEntity)
}
