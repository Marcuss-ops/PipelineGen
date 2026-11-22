package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompleteGroundedPhraseCompletesTheContainingSentence(t *testing.T) {
	text := "La pena divenne definitiva; poi la cronologia proseguì."
	whole, ok := findEntitySpan(text, "La pena divenne definitiva")
	require.True(t, ok)
	whole, ok = completeGroundedPhrase(text, whole)
	require.True(t, ok)
	require.Equal(t, "La pena divenne definitiva;", whole.Text)

	fragment, ok := findEntitySpan(text, "pena divenne")
	require.True(t, ok)
	fragment, ok = completeGroundedPhrase(text, fragment)
	require.True(t, ok)
	require.Equal(t, "La pena divenne definitiva;", fragment.Text)

	endingFragment, ok := findEntitySpan(text, "la cronologia")
	require.True(t, ok)
	endingFragment, ok = completeGroundedPhrase(text, endingFragment)
	require.True(t, ok)
	require.Equal(t, "poi la cronologia proseguì.", endingFragment.Text)
}

func TestCompleteGroundedPhraseKeepsEnglishMidSentenceSelections(t *testing.T) {
	text := "In 1991, Mike Tyson was already a highly recognizable figure in American sports. " +
		"Tyson denied the accusation, maintaining that the encounter had been consensual."
	fragment, ok := findEntitySpan(text, "recognizable figure in American sports")
	require.True(t, ok)
	fragment, ok = completeGroundedPhrase(text, fragment)
	require.True(t, ok)
	require.Equal(t, "In 1991, Mike Tyson was already a highly recognizable figure in American sports.", fragment.Text)
}
