package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompleteGroundedPhraseRequiresWholeSentenceBoundary(t *testing.T) {
	text := "La pena divenne definitiva; poi la cronologia proseguì."
	whole, ok := findEntitySpan(text, "La pena divenne definitiva")
	require.True(t, ok)
	whole, ok = completeGroundedPhrase(text, whole)
	require.True(t, ok)
	require.Equal(t, "La pena divenne definitiva;", whole.Text)

	fragment, ok := findEntitySpan(text, "pena divenne")
	require.True(t, ok)
	_, ok = completeGroundedPhrase(text, fragment)
	require.False(t, ok)

	endingFragment, ok := findEntitySpan(text, "la cronologia")
	require.True(t, ok)
	_, ok = completeGroundedPhrase(text, endingFragment)
	require.False(t, ok)
}
