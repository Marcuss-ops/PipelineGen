package scriptgeneration

// End-to-end certification: Rust signals → Go planning → admission gates →
// render-ready invariants. One semantic index, no duplicate extraction,
// coherent admission under density, and a final plan with nothing outside
// certified timing and no prohibited collision.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func siSceneSignals() scriptpkg.SISceneSignals {
	year := 2020
	return scriptpkg.SISceneSignals{
		SceneID: "scene-08",
		Density: scriptpkg.SIDensity{SceneID: "scene-08", Score: 0.85, Level: "high", SuggestedVisualBudget: "low"},
		Comparisons: []scriptpkg.SIComparison{{
			SceneID: "scene-08", Kind: "comparison", Subjects: []string{"ford", "toyota"},
			Dimension: "general", Relation: scriptpkg.RelationContrastsWith,
			EvidenceSentenceIDs: []int{7}, CandidateVisual: "split_comparison",
		}},
		Causal: []scriptpkg.SICausalRelation{{
			Kind: "causal_relation", Cause: "costi", Effect: "margini", Relation: scriptpkg.RelationCauses,
			EvidenceSentenceIDs: []int{8}, ExtractionConfidence: 0.9,
		}},
		TemporalEvents: []scriptpkg.SITemporalEvent{{
			EventID: "event-0008", TextSpan: "Nel 2020 i costi aumentarono.",
			TemporalExpression: "2020", NormalizedYear: &year, NarrationIndex: 8,
		}},
	}
}

func TestSIX01SingleIndexNoDuplicateExtraction(t *testing.T) {
	// X01: one scene with numbers, a comparison and a causal link → one
	// candidate per signal, evidence preserved, nothing duplicated.
	sig := siSceneSignals()
	cands := capabilityoverlay.ComparisonCandidates(
		sig.SceneID, sig.Comparisons[0].Subjects, sig.Comparisons[0].CandidateVisual,
		sig.Comparisons[0].Values, sig.Comparisons[0].EvidenceSentenceIDs, 0.8, false)
	cands = append(cands, capabilityoverlay.CausalCandidates(
		sig.SceneID, sig.Causal[0].Cause, sig.Causal[0].Effect, sig.Causal[0].Relation,
		sig.Causal[0].EvidenceSentenceIDs, 0.9, false)...)
	require.Len(t, cands, 2, "one candidate per asserted signal")
	assert.Equal(t, []int{7}, cands[0].Evidence)
	assert.Equal(t, []int{8}, cands[1].Evidence, "evidence ids travel with their signal")
	seen := map[string]struct{}{}
	for _, c := range cands {
		key := string(c.Category) + "\x00" + c.Text
		_, dup := seen[key]
		assert.False(t, dup, "no duplicated extraction")
		seen[key] = struct{}{}
	}
}

func TestSIX02TenCandidatesCoherentAdmission(t *testing.T) {
	// X02: 10 visual candidates under a dense-scene budget → admission
	// honors requested priority, density-narrowed budget and collisions.
	win := func(s, e int64) *scriptpkg.TimelineWindow {
		return &scriptpkg.TimelineWindow{StartMS: s, EndMS: e}
	}
	budget := capabilityoverlay.DensityOverlapBudget(0.85, 3)
	cands := []scriptoverlay.VisualCandidate{
		{CanonicalID: "s:chart-00", Kind: scriptoverlay.VisualChart, SceneID: "s", Requested: false, Window: win(0, 4000), Score: 0.9},
		{CanonicalID: "s:image-00", Kind: scriptoverlay.VisualImage, SceneID: "s", Requested: false, Window: win(0, 2000), Score: 0.85},
		{CanonicalID: "s:image-01", Kind: scriptoverlay.VisualImage, SceneID: "s", Requested: false, Window: win(1000, 3000), Score: 0.8},
		{CanonicalID: "s:image-02", Kind: scriptoverlay.VisualImage, SceneID: "s", Requested: false, Window: win(2000, 4000), Score: 0.75},
		{CanonicalID: "s:hl-00", Kind: scriptoverlay.VisualHighlight, SceneID: "s", Requested: true, Window: win(500, 1500), Score: 0.1},
		{CanonicalID: "s:hl-01", Kind: scriptoverlay.VisualHighlight, SceneID: "s", Requested: false, Window: win(600, 1600), Score: 0.95},
		{CanonicalID: "s:hl-02", Kind: scriptoverlay.VisualHighlight, SceneID: "s", Requested: false, Window: win(2500, 3500), Score: 0.7},
		{CanonicalID: "s:hl-03", Kind: scriptoverlay.VisualHighlight, SceneID: "s", Requested: false, Window: nil, Score: 0.99},
		{CanonicalID: "s:img-03", Kind: scriptoverlay.VisualImage, SceneID: "s", Requested: false, Window: win(3000, 5000), Score: 0.6},
		{CanonicalID: "s:hl-04", Kind: scriptoverlay.VisualHighlight, SceneID: "s", Requested: false, Window: win(3600, 4600), Score: 0.65},
	}
	got := scriptoverlay.AdmitVisuals(cands, scriptoverlay.AdmissionBudget{
		MaxPerScene: budget, MaxCharts: 1, MaxImages: 2, MaxHighlights: 2,
	})
	byID := map[string]struct{}{}
	for _, a := range got {
		byID[a.CanonicalID] = struct{}{}
	}
	assert.Contains(t, byID, "s:hl-00", "requested priority survives collision despite lowest score")
	assert.NotContains(t, byID, "s:hl-01", "same-kind overlap loses to requested")
	assert.NotContains(t, byID, "s:hl-03", "windowless candidates are data only")
	assert.LessOrEqual(t, len(got), budget, "density-narrowed budget respected")
}

func TestSIX10FinalPlanInsideTimingWithoutCollisions(t *testing.T) {
	// X10: charts + recap + overlays in one scene → every admitted item
	// inside certified timing, no same-kind overlap reaches the renderer.
	const sceneStart, sceneEnd int64 = 0, 20000
	opts := []scriptpkg.OptCandidate{
		{ID: "chart", SceneID: "s", R: 0.9, Q: 0.9, StartMS: 2000, EndMS: 8000, AnchorMS: 2000, MinDurationMS: 1000},
		{ID: "recap", SceneID: "s", R: 0.8, Q: 0.85, StartMS: 9000, EndMS: 14000, AnchorMS: 9000, MinDurationMS: 1000},
		{ID: "early", SceneID: "s", R: 0.99, Q: 0.99, StartMS: 1000, EndMS: 5000, AnchorMS: 3000, MinDurationMS: 500},
		{ID: "brief", SceneID: "s", R: 0.7, Q: 0.7, StartMS: 15000, EndMS: 15200, AnchorMS: 15000, MinDurationMS: 800},
	}
	admitted, _ := scriptpkg.SelectLocal(opts, scriptpkg.DefaultOptWeights(), 2)
	ids := map[string]struct{}{}
	for _, a := range admitted {
		ids[a.ID] = struct{}{}
		assert.GreaterOrEqual(t, a.StartMS, sceneStart)
		assert.LessOrEqual(t, a.EndMS, sceneEnd, "nothing outside certified timing")
		assert.GreaterOrEqual(t, a.StartMS, a.AnchorMS, "nothing before its spoken anchor")
	}
	assert.NotContains(t, ids, "early", "anchor violators never reach render")
	assert.NotContains(t, ids, "brief", "illegible durations never reach render")
	assert.Contains(t, ids, "chart")
	assert.Contains(t, ids, "recap")
}
