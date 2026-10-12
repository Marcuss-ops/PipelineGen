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

func TestSIGroundedProjectionRequiresSentenceEvidenceAndPlaybackWindow(t *testing.T) {
	manifest := siTestManifest()
	manifest.Scenes = manifest.Scenes[:1]
	manifest.Scenes[0].TemporalEvents[0].TextSpan = "Nel 2020 i costi aumentarono."
	manifest.Scenes[0].TemporalEvents[0].TemporalExpression = "2020"
	manifest.Scenes[0].Comparisons[0].EvidenceSentenceIDs = []int{1}
	input := scriptpkg.SISceneAnalysisInput{
		SceneID: "scene-01", Text: "Nel 2020 i costi aumentarono. Ford contrasts with Toyota.",
		VideoStartMS: 250, VideoEndMS: 1750,
	}

	got := ProjectGroundedSceneIntelligence(manifest, SIProjectionConfig{Enabled: true}, []scriptpkg.SISceneAnalysisInput{input}, nil)
	require.Len(t, got, 1)
	assert.Equal(t, int64(250), got[0].VideoStartMS)
	assert.Equal(t, int64(1750), got[0].VideoEndMS)
	assert.True(t, got[0].TimelineRecipeEligible)
	assert.Contains(t, got[0].CandidateKinds, "multi_entity", "the comparison's sentence index is valid and its subjects are grounded")

	manifest.Scenes[0].TemporalEvents = nil
	manifest.Scenes[0].Comparisons[0].EvidenceSentenceIDs = []int{0}
	assert.Empty(t, ProjectGroundedSceneIntelligence(manifest, SIProjectionConfig{Enabled: true}, []scriptpkg.SISceneAnalysisInput{input}, nil),
		"a valid but unrelated evidence sentence must not ground the comparison")

	manifest.Scenes[0].Comparisons[0].EvidenceSentenceIDs = []int{2}
	manifest.Scenes[0].TemporalEvents = nil
	assert.Empty(t, ProjectGroundedSceneIntelligence(manifest, SIProjectionConfig{Enabled: true}, []scriptpkg.SISceneAnalysisInput{input}, nil),
		"evidence outside the scene sentence index must not project")
	assert.Empty(t, ProjectGroundedSceneIntelligence(manifest, SIProjectionConfig{Enabled: true}, []scriptpkg.SISceneAnalysisInput{{
		SceneID: "scene-01", Text: input.Text, VideoStartMS: 1000, VideoEndMS: 1000,
	}}, nil), "zero-duration playback windows must not project")
}

func TestSIGroundedProjectionRejectsDuplicateWindowsAndMismatchedNormalizedYear(t *testing.T) {
	manifest := siTestManifest()
	manifest.Scenes = manifest.Scenes[:1]
	input := scriptpkg.SISceneAnalysisInput{
		SceneID: "scene-01", Text: "Nel 2020 i costi aumentarono.",
		VideoStartMS: 100, VideoEndMS: 1100,
	}
	assert.Nil(t, ProjectGroundedSceneIntelligence(manifest, SIProjectionConfig{Enabled: true}, []scriptpkg.SISceneAnalysisInput{input, input}, nil),
		"duplicate scene-to-window bindings are ambiguous and must fail closed")

	wrongYear := 2019
	manifest.Scenes[0].TemporalEvents[0].NormalizedYear = &wrongYear
	assert.Empty(t, ProjectGroundedSceneIntelligence(manifest, SIProjectionConfig{Enabled: true}, []scriptpkg.SISceneAnalysisInput{input}, nil),
		"a normalized year that differs from the verbatim temporal expression must not become eligible")

	manifest.SchemaVersion = "unknown.v99"
	assert.Nil(t, ProjectGroundedSceneIntelligence(manifest, SIProjectionConfig{Enabled: true}, []scriptpkg.SISceneAnalysisInput{input}, nil),
		"an invalid manifest must never be partially projected")
}

func TestSIGroundedProjectionRejectsUngroundedTemporalAndNegatedSignals(t *testing.T) {
	manifest := siTestManifest()
	manifest.Scenes = manifest.Scenes[:1]
	manifest.Scenes[0].Comparisons[0].Negated = true
	manifest.Scenes[0].TemporalEvents[0].TextSpan = "Ford won in 2020."
	input := scriptpkg.SISceneAnalysisInput{
		SceneID: "scene-01", Text: "Ford contrasts with Toyota. No dates are mentioned.",
		VideoStartMS: 0, VideoEndMS: 2000,
	}
	assert.Empty(t, ProjectGroundedSceneIntelligence(manifest, SIProjectionConfig{Enabled: true}, []scriptpkg.SISceneAnalysisInput{input}, nil),
		"negated comparisons and temporal spans absent from narration must be excluded")
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
