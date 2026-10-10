package overlay

// Acceptance: six competing candidates resolve with no illegal overlap and
// no lost explicit priority.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func win(start, end int64) *scriptpkg.TimelineWindow {
	return &scriptpkg.TimelineWindow{StartMS: start, EndMS: end}
}

func TestAdmitSixCompetitors(t *testing.T) {
	cands := []VisualCandidate{
		{CanonicalID: "scene-1:chart-00", Kind: VisualChart, SceneID: "scene-1", Requested: false, Window: win(1000, 4000), Score: 0.9},
		{CanonicalID: "scene-1:image-00", Kind: VisualImage, SceneID: "scene-1", Requested: false, Window: win(1000, 3000), Score: 0.8},
		{CanonicalID: "scene-1:image-01", Kind: VisualImage, SceneID: "scene-1", Requested: false, Window: win(2000, 5000), Score: 0.7},
		{CanonicalID: "scene-1:highlight-00", Kind: VisualHighlight, SceneID: "scene-1", Requested: true, Window: win(1500, 2500), Score: 0.1},
		{CanonicalID: "scene-1:highlight-01", Kind: VisualHighlight, SceneID: "scene-1", Requested: false, Window: win(1600, 2600), Score: 0.95},
		{CanonicalID: "scene-1:highlight-02", Kind: VisualHighlight, SceneID: "scene-1", Requested: false, Window: nil, Score: 0.99},
	}
	got := AdmitVisuals(cands, AdmissionBudget{MaxPerScene: 3, MaxCharts: 1, MaxImages: 1, MaxHighlights: 2})
	byID := map[string]AdmittedVisual{}
	for _, a := range got {
		byID[a.CanonicalID] = a
	}
	// Requested highlight wins its collision despite the lowest score.
	require.Contains(t, byID, "scene-1:highlight-00", "explicit priority must survive collision")
	assert.NotContains(t, byID, "scene-1:highlight-01", "same-kind overlap loses")
	assert.NotContains(t, byID, "scene-1:highlight-02", "windowless candidates are data only")
	// Image cap 1: higher score wins; chart admitted once.
	require.Contains(t, byID, "scene-1:image-00")
	assert.NotContains(t, byID, "scene-1:image-01")
	assert.LessOrEqual(t, len(got), 3, "scene budget respected")
}

func TestAdmitIsOrderIndependent(t *testing.T) {
	base := []VisualCandidate{
		{CanonicalID: "s:highlight-00", Kind: VisualHighlight, SceneID: "s", Window: win(0, 500), Score: 0.5},
		{CanonicalID: "s:highlight-01", Kind: VisualHighlight, SceneID: "s", Window: win(600, 900), Score: 0.6},
		{CanonicalID: "s:chart-00", Kind: VisualChart, SceneID: "s", Window: win(0, 900), Score: 0.4},
	}
	a := AdmitVisuals(base, AdmissionBudget{})
	rev := []VisualCandidate{base[2], base[1], base[0]}
	b := AdmitVisuals(rev, AdmissionBudget{})
	require.Equal(t, a, b, "admission must be deterministic regardless of input order")
}
