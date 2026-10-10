package script

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func graphFixture(t *testing.T) (*InvalidationGraph, string, string) {
	t.Helper()
	g := NewInvalidationGraph()
	scene4v1, err := FingerprintBytes("scene-04 text v1")
	require.NoError(t, err)
	scene3, err := FingerprintBytes("scene-03 text")
	require.NoError(t, err)
	profile, err := FingerprintBytes("highlights.v1")
	require.NoError(t, err)
	g.Upsert(DepNode{ID: "editorial:scene-03", Inputs: []string{scene3, profile}, Product: "ed3"})
	g.Upsert(DepNode{ID: "editorial:scene-04", Inputs: []string{scene4v1, profile}, Product: "ed4"})
	g.Upsert(DepNode{ID: "timeline:scene-03:it", Inputs: []string{scene3, "tts:it"}, Product: "tl3"})
	g.Upsert(DepNode{ID: "chapters:job", Inputs: []string{scene3, scene4v1, profile}, Product: "ch", Global: true})
	return g, scene4v1, scene3
}

func TestInvalidationScene4Change(t *testing.T) {
	g, scene4v1, _ := graphFixture(t)

	// Scene 4 edited: its recorded v1 input fingerprint is no longer
	// current. Dependents of v1 invalidate; scene-03 products do not.
	invalid := g.Invalidate([]string{scene4v1})
	assert.Contains(t, invalid, "editorial:scene-04", "edited scene rebuilds")
	assert.Contains(t, invalid, "chapters:job", "global products invalidate on any scene change")
	assert.NotContains(t, invalid, "editorial:scene-03", "independent scenes must not invalidate")
	assert.NotContains(t, invalid, "timeline:scene-03:it")
}

func TestInvalidationUnchangedRerunIsEmpty(t *testing.T) {
	g, _, _ := graphFixture(t)
	assert.Empty(t, g.Invalidate(nil), "no changes means no rebuilds")
	assert.Empty(t, g.Invalidate([]string{"unrelated-input"}))
}

func TestFingerprintCoversFullInput(t *testing.T) {
	a, err := FingerprintBytes(map[string]string{"scene": "x", "topic": "jobs"})
	require.NoError(t, err)
	b, err := FingerprintBytes(map[string]string{"scene": "x", "topic": "cars"})
	require.NoError(t, err)
	assert.NotEqual(t, a, b)
}
