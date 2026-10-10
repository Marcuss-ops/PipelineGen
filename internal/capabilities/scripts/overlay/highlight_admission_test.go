package overlay

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdmissionRequestedBeatsIdenticalExtracted(t *testing.T) {
	req := []HighlightCandidate{{Text: "Potenza e disciplina", SceneID: "s1"}}
	ext := []HighlightCandidate{{Text: "potenza e DISCIPLINA", SceneID: "s1", Score: 9.9}}
	got := ResolveHighlightAdmission(req, ext, DefaultAdmissionConfig())
	require.Len(t, got, 1)
	assert.Equal(t, "requested", got[0].Kind)
}

func TestAdmissionBudgetCapsExtracted(t *testing.T) {
	ext := []HighlightCandidate{
		{Text: "alpha", SceneID: "s1", Score: 1},
		{Text: "beta", SceneID: "s1", Score: 3},
		{Text: "gamma", SceneID: "s1", Score: 2},
	}
	got := ResolveHighlightAdmission(nil, ext, AdmissionConfig{MaxPerScene: 2})
	require.Len(t, got, 2)
	assert.Equal(t, "beta", got[0].Text)
	assert.Equal(t, "gamma", got[1].Text)
}

func TestAdmissionFullRequestedLeavesNoRoom(t *testing.T) {
	req := []HighlightCandidate{
		{Text: "one", SceneID: "s1"}, {Text: "two", SceneID: "s1"},
	}
	ext := []HighlightCandidate{{Text: "three", SceneID: "s1", Score: 9}}
	got := ResolveHighlightAdmission(req, ext, AdmissionConfig{MaxPerScene: 2})
	require.Len(t, got, 2)
	for _, g := range got {
		assert.Equal(t, "requested", g.Kind)
	}
}

func TestAdmissionEmptyIsEmpty(t *testing.T) {
	assert.Empty(t, ResolveHighlightAdmission(nil, nil, DefaultAdmissionConfig()))
}
