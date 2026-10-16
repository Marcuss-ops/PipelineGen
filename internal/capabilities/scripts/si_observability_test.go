package scriptgeneration

import (
	"encoding/json"
	caprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/render"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func siObsSignals() []scriptpkg.SISceneSignals {
	return []scriptpkg.SISceneSignals{
		{SceneID: "s1",
			Density:     scriptpkg.SIDensity{SceneID: "s1", Score: 0.85, Level: "high"},
			Comparisons: []scriptpkg.SIComparison{{SceneID: "s1", Kind: "comparison"}},
			Causal:      []scriptpkg.SICausalRelation{{Kind: "causal_relation", Relation: "causes"}},
		},
		{SceneID: "s2",
			Density:        scriptpkg.SIDensity{SceneID: "s2", Score: 0.2, Level: "low"},
			TemporalEvents: []scriptpkg.SITemporalEvent{{EventID: "e1"}},
			Relations:      []scriptpkg.SIEntityRelation{{Subject: "a", Object: "b", Relation: "partners_with"}},
		},
		{SceneID: "s3", Density: scriptpkg.SIDensity{SceneID: "s3", Score: 0.5, Level: "medium"}},
	}
}

func siObsPlan() capabilityoverlay.OverlayPlan {
	mk := func(id, scene string, startMS, endMS int64, recap bool) capabilityoverlay.OverlayItem {
		params := map[string]any{}
		if recap {
			params["recap"] = true
		}
		return capabilityoverlay.OverlayItem{ID: id, SceneID: scene, TemplateID: "QUOTE", Text: id,
			StartMs: startMS, EndMs: endMS, StartUS: startMS * 1000, DurationUS: (endMS - startMS) * 1000,
			Params: params}
	}
	return capabilityoverlay.OverlayPlan{Items: []capabilityoverlay.OverlayItem{
		mk("q1", "s1", 0, 4000, false),
		mk("r1", "s2", 20000, 24000, true),
		mk("r2", "s2", 24000, 28000, true),
	}}
}

func TestBuildSIJobReportJoinsSignalsAndItems(t *testing.T) {
	rep := BuildSIJobReport(SIReportInput{
		PlanID: "p", VideoID: "v",
		Signals: siObsSignals(),
		Placements: []capabilityoverlay.RecapPlacement{
			{SceneID: "s2", WindowStartUS: 20_000_000, WindowEndUS: 40_000_000, NarratedThrough: 1},
			{SceneID: "s3", WindowStartUS: 40_000_000, WindowEndUS: 60_000_000, NarratedThrough: 2},
		},
		Plan: siObsPlan(),
		Windows: []caprender.SceneWindow{
			{SceneID: "s1", StartUS: 0, EndUS: 20_000_000},
			{SceneID: "s2", StartUS: 20_000_000, EndUS: 40_000_000},
			{SceneID: "s3", StartUS: 40_000_000, EndUS: 60_000_000},
		},
		DeadAirMinGapUS: 5_000_000,
	})
	require.Len(t, rep.Scenes, 3, "every signal scene gets a row, in narration order")
	assert.Equal(t, "s1", rep.Scenes[0].SceneID)
	assert.Equal(t, "s2", rep.Scenes[1].SceneID)
	assert.Equal(t, "s3", rep.Scenes[2].SceneID)

	s1 := rep.Scenes[0]
	assert.Equal(t, 0.85, s1.DensityScore)
	assert.Equal(t, "high", s1.DensityLevel)
	assert.Equal(t, 1, s1.Comparisons)
	assert.Equal(t, 1, s1.Causal)
	assert.Equal(t, 1, s1.OverlayItems)
	assert.Equal(t, "no-placement-for-scene", s1.RecapSkipReason)

	s2 := rep.Scenes[1]
	assert.True(t, s2.RecapPlaced)
	assert.Equal(t, 2, s2.RecapCards)
	assert.Empty(t, s2.RecapSkipReason)
	assert.Equal(t, 1, s2.TemporalEvents)
	assert.Equal(t, 1, s2.EntityRelations)

	s3 := rep.Scenes[2]
	assert.False(t, s3.RecapPlaced)
	assert.Equal(t, "placement-attempted-no-eligible-cards", s3.RecapSkipReason)
	assert.Equal(t, 0, s3.OverlayItems)

	assert.Equal(t, 1, rep.TotalComparisons)
	assert.Equal(t, 1, rep.TotalCausal)
	assert.Equal(t, 1, rep.TotalTemporalEvents)
	assert.Equal(t, 1, rep.TotalEntityRel)
	assert.Equal(t, 2, rep.TotalRecapCards)
	assert.Equal(t, 1, rep.ScenesWithRecap)
	assert.GreaterOrEqual(t, rep.DeadAirGaps, 1, "naked stretches are counted")

	j, err := json.Marshal(rep)
	require.NoError(t, err, "report is JSON-serializable for the future sink")
	assert.Contains(t, string(j), "placement-attempted-no-eligible-cards")
}

func TestBuildSIJobReportEmpty(t *testing.T) {
	rep := BuildSIJobReport(SIReportInput{PlanID: "p", VideoID: "v"})
	assert.Empty(t, rep.Scenes)
	assert.Zero(t, rep.TotalRecapCards)
	assert.Zero(t, rep.DeadAirGaps)
}
