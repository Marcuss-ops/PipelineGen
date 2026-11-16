package scriptgeneration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWholeScenePhraseTextRejectsLongNarration(t *testing.T) {
	atLimit := strings.TrimSpace(strings.Repeat("parola ", maxWholeScenePhraseWords))
	got, ok := wholeScenePhraseText("  " + atLimit + "  ")
	require.True(t, ok)
	require.Equal(t, atLimit, got)

	_, ok = wholeScenePhraseText(atLimit + " oltre")
	require.False(t, ok, "a phrase over the callout ceiling must be rejected")

	_, ok = wholeScenePhraseText("   ")
	require.False(t, ok)
}
