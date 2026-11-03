package entitytype

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFoldIsTheSingleSpellingTable pins the contract every layer projects:
// one family per real-world thing, unknown spellings left alone (never
// guessed), and a closed canonical vocabulary stable under Fold.
func TestFoldIsTheSingleSpellingTable(t *testing.T) {
	require.Equal(t, Place, Fold("GPE"))
	require.Equal(t, Place, Fold(" location "))
	require.Equal(t, Place, Fold("LOC"))
	require.Equal(t, Place, Fold("FAC"))
	require.Equal(t, Org, Fold("COMPANY"))
	require.Equal(t, Org, Fold("COURT"))
	require.Equal(t, Logo, Fold("BRAND"))
	require.Equal(t, WorkOfArt, Fold("WORK"))
	// One identity for one number, whatever the extractor called it.
	require.Equal(t, Number, Fold("CARDINAL"))
	require.Equal(t, Number, Fold("ORDINAL"))
	require.Equal(t, Number, Fold("METRIC"))

	require.Equal(t, "SATELLITE", Fold(" satellite "))
	require.Equal(t, "", Fold("   "))
}

func TestFamiliesAreClosedAndStable(t *testing.T) {
	families := Families()
	require.NotEmpty(t, families)
	for _, family := range families {
		require.True(t, IsCanonical(family), "declared family %q is not in the closed set", family)
		require.Equal(t, family, Fold(family), "family %q must fold onto itself", family)
	}
	for spelling, family := range AliasSpellings() {
		require.True(t, IsCanonical(family), "spelling %q targets undeclared family %q", spelling, family)
		require.Equal(t, family, Fold(spelling))
	}
}

func TestIsNeverMatchesABlankType(t *testing.T) {
	require.False(t, Is("", Place))
	require.False(t, Is("   ", Place))
	require.True(t, Is("GPE", Place))
	require.False(t, Is("PERSON", Place))
}

func TestAliasesEnumeratesEverySpellingOfOneFamily(t *testing.T) {
	spellings := Aliases("gpe")
	require.Equal(t, Place, spellings[0], "canonical spelling is first")
	require.Contains(t, spellings, "GPE")
	require.Contains(t, spellings, "LOCATION")
	require.Contains(t, spellings, "FACILITY")
	require.Equal(t, Aliases("LOCATION"), spellings, "every spelling shares one enumeration")
	require.Equal(t, []string{"SATELLITE"}, Aliases("satellite"))
	require.Nil(t, Aliases(""))
}
