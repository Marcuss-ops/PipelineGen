package script

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdentitySameHighlightAcrossLanguages(t *testing.T) {
	r := NewIdentityRegistry("en")
	id, err := r.Register("scene-03", EditorialKindHighlight, "Industrial employment")
	require.NoError(t, err)
	require.NoError(t, r.Localize(id, "it", "Occupazione industriale"))
	require.NoError(t, r.Localize(id, "de", "Industriebeschäftigung"))
	require.NoError(t, r.AnchorTiming(id, "it", 15200, 18600))
	require.NoError(t, r.AnchorTiming(id, "de", 15800, 19300))

	it, ok := r.GetLocalized(id, "it")
	require.True(t, ok)
	de, ok := r.GetLocalized(id, "de")
	require.True(t, ok)
	assert.Equal(t, id, it.CanonicalID, "identity survives translation")
	assert.Equal(t, id, de.CanonicalID)
	assert.NotEqual(t, it.StartMS, de.StartMS, "local timing may differ per language")
	assert.Equal(t, "Occupazione industriale", it.Text)
}

func TestIdentityLocalizeUnknownFailsClosed(t *testing.T) {
	r := NewIdentityRegistry("en")
	bad, _ := MakeCanonicalID("scene-01", EditorialKindHighlight, 7)
	assert.Error(t, r.Localize(bad, "it", "x"), "translations must never mint identity")
	assert.Error(t, r.AnchorTiming(bad, "it", 1, 2))
}

func TestCanonicalIDRoundTrip(t *testing.T) {
	id, err := MakeCanonicalID("scene-03", EditorialKindHighlight, 2)
	require.NoError(t, err)
	require.Equal(t, CanonicalID("scene-03:highlight-02"), id)
	scene, kind, n, err := ParseCanonicalID(id)
	require.NoError(t, err)
	assert.Equal(t, "scene-03", scene)
	assert.Equal(t, EditorialKindHighlight, kind)
	assert.Equal(t, 2, n)
	_, _, _, err = ParseCanonicalID("bogus")
	assert.Error(t, err)
}
