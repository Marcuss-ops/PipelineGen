package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func siTestManifest() *scriptpkg.SceneIntelligenceManifest {
	year := 2020
	return &scriptpkg.SceneIntelligenceManifest{
		SchemaVersion:           scriptpkg.SceneIntelligenceManifestVersion,
		AnalyzerVersion:         scriptpkg.SceneIntelligenceAnalyzerVersion,
		RelationRegistryVersion: scriptpkg.RelationRegistryVersion,
		Scenes: []scriptpkg.SISceneSignals{
			{
				SceneID: "scene-01",
				Density: scriptpkg.SIDensity{SceneID: "scene-01", Score: 0.2, SuggestedVisualBudget: "standard"},
				Comparisons: []scriptpkg.SIComparison{{
					SceneID: "scene-01", Kind: "comparison", Subjects: []string{"ford", "toyota"},
					Relation: scriptpkg.RelationContrastsWith, CandidateVisual: "split_comparison",
					EvidenceSentenceIDs: []int{0},
				}},
				TemporalEvents: []scriptpkg.SITemporalEvent{{
					EventID: "event-0000", TextSpan: "Nel 2020 i costi aumentarono.",
					TemporalExpression: "2020", NormalizedYear: &year, NarrationIndex: 0,
				}},
			},
			{
				SceneID: "scene-02",
				Density: scriptpkg.SIDensity{SceneID: "scene-02", Score: 0.85, SuggestedVisualBudget: "low"},
			},
		},
	}
}

func TestSIProjectionDisabledIsNoop(t *testing.T) {
	got := ProjectSceneIntelligence(siTestManifest(), SIProjectionConfig{}, map[string]bool{"scene-01": true})
	assert.Nil(t, got, "disabled shadow mode must not project anything: job outputs stay identical")
	got = ProjectSceneIntelligence(nil, SIProjectionConfig{Enabled: true}, nil)
	assert.Nil(t, got, "nil manifest projects nothing")
}

func TestSIProjectionMapsSignals(t *testing.T) {
	got := ProjectSceneIntelligence(siTestManifest(), SIProjectionConfig{Enabled: true}, map[string]bool{"scene-01": true})
	require.Len(t, got, 2)
	assert.Equal(t, "scene-01", got[0].SceneID, "deterministic scene order")
	assert.Contains(t, got[0].CandidateKinds, "multi_entity", "qualitative comparison → split-screen candidate")
	assert.True(t, got[0].TimelineRecipeEligible, "absolute years make a timeline recipe eligible")
	assert.True(t, got[0].RecapEligible)
	assert.Equal(t, "low", got[1].VisualBudget, "dense scene hints few simultaneous overlays")
	assert.False(t, got[1].RecapEligible)

	again := ProjectSceneIntelligence(siTestManifest(), SIProjectionConfig{Enabled: true}, map[string]bool{"scene-01": true})
	assert.Equal(t, got, again, "projection is deterministic")
}
