package entities

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalEntityID_Format(t *testing.T) {
	cases := []struct {
		name       string
		entityType string
		value      string
		want       string
	}{
		{"person with suffix", "PERSON", "Floyd Mayweather Jr.", "person:floyd-mayweather-jr"},
		{"plain person", "PERSON", "Floyd Mayweather", "person:floyd-mayweather"},
		{"organization", "ORGANIZATION", "Apple Inc.", "organization:apple-inc"},
		{"location", "LOCATION", "Los Angeles", "location:los-angeles"},
		{"image entity", "IMAGE_ENTITY", "Mount Rushmore", "image_entity:mount-rushmore"},
		{"type case-insensitive", "person", "Tim Cook", "person:tim-cook"},
		{"name whitespace-insensitive", "PERSON", "  TIM   COOK ", "person:tim-cook"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, CanonicalEntityID(tc.entityType, tc.value))
		})
	}
}

func TestCanonicalEntityID_EmptyName(t *testing.T) {
	require.Equal(t, "", CanonicalEntityID("PERSON", ""))
	require.Equal(t, "", CanonicalEntityID("PERSON", "   "))
	// Only punctuation collapses to an empty slug — no "person:" is minted.
	require.Equal(t, "", CanonicalEntityID("PERSON", "..."))
}

func TestCanonicalEntityID_Deterministic(t *testing.T) {
	a := CanonicalEntityID("PERSON", "Floyd Mayweather Jr.")
	b := CanonicalEntityID("PERSON", "floyd  mayweather   jr.")
	require.Equal(t, a, b)
}

func TestIsEntityType(t *testing.T) {
	entityTypes := []SemanticType{
		SemanticPerson, SemanticOrganization, SemanticLocation, SemanticImageEntity,
	}
	for _, typ := range entityTypes {
		require.True(t, IsEntityType(typ), "expected %q to be an entity type", typ)
	}
	nonEntityTypes := []SemanticType{
		SemanticDate, SemanticMoney, SemanticNumber, SemanticPercentage,
		SemanticImportantPhrase, SemanticQuote, SemanticClaim, SemanticStatistic,
		SemanticRanking, SemanticTitle, SemanticEvent,
	}
	for _, typ := range nonEntityTypes {
		require.False(t, IsEntityType(typ), "expected %q NOT to be an entity type", typ)
	}
}

func TestCanonicalEntityResolver_ResolveEntityType(t *testing.T) {
	item := validSemanticItem()
	resolved := DefaultCanonicalEntityResolver.Resolve(item)

	require.Equal(t, "person:floyd-mayweather", resolved.CanonicalEntityID)
	// extractor-provided normalized_text is preserved, not overwritten.
	require.Equal(t, "floyd mayweather", resolved.NormalizedText)
	// the input is never mutated.
	require.Equal(t, "", item.CanonicalEntityID)
	require.NoError(t, resolved.Validate())
}

func TestCanonicalEntityResolver_ResolveNonEntityType(t *testing.T) {
	item := validSemanticItem()
	item.Type = SemanticImportantPhrase
	resolved := DefaultCanonicalEntityResolver.Resolve(item)

	require.Equal(t, "", resolved.CanonicalEntityID)
	require.Equal(t, SemanticImportantPhrase, resolved.Type)
}

func TestCanonicalEntityResolver_DerivesMissingNormalizedText(t *testing.T) {
	item := validSemanticItem()
	item.NormalizedText = ""
	resolved := DefaultCanonicalEntityResolver.Resolve(item)

	require.Equal(t, "floyd mayweather", resolved.NormalizedText)
	require.Equal(t, "person:floyd-mayweather", resolved.CanonicalEntityID)
}

func TestCanonicalEntityResolver_ClearsStaleIDOnNonEntity(t *testing.T) {
	item := validSemanticItem()
	item.Type = SemanticQuote
	item.CanonicalEntityID = "stale:value"
	resolved := DefaultCanonicalEntityResolver.Resolve(item)

	require.Equal(t, "", resolved.CanonicalEntityID)
}

func TestCanonicalEntityResolver_ResolveAll(t *testing.T) {
	person := validSemanticItem()
	money := validSemanticItem()
	money.SemanticID = "sem_scene03_money_01"
	money.Type = SemanticMoney
	money.Text = "$100 million"
	money.NormalizedText = "$100 million"

	resolved := DefaultCanonicalEntityResolver.ResolveAll([]SemanticItem{person, money})

	require.Len(t, resolved, 2)
	require.Equal(t, "person:floyd-mayweather", resolved[0].CanonicalEntityID)
	require.Equal(t, "", resolved[1].CanonicalEntityID)
}
