package scriptgeneration

// Live-wiring certification for automatic recap cards: the scene sequence
// maps to recap placements at chapter boundaries, and placements compile
// through BuildPlan (overlap degradation + plan validation) like any other
// overlay item.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func recapBriefs5() []recapSceneBrief {
	titles := []string{
		"Nel 2020 fu annunciato il progetto",
		"L'aumento dei costi ha ridotto i margini",
		"La crisi non è stata causata dall'inflazione",
		"Nel 2024 il progetto venne completato",
		"La ripresa supera le attese",
	}
	briefs := make([]recapSceneBrief, 0, 5)
	for i, title := range titles {
		briefs = append(briefs, recapSceneBrief{
			id: title[:2] + string(rune('1'+i)), title: title, score: 0.8,
			startUS: int64(i) * 20_000_000, endUS: int64(i+1) * 20_000_000,
		})
	}
	return briefs
}

func TestRecapWiringFiveScenesOnePlacement(t *testing.T) {
	got := recapPlacementsForScenes(recapBriefs5())
	require.Len(t, got, 1, "5 scenes → recap after chapter 3")
	p := got[0]
	assert.Equal(t, 2, p.NarratedThrough, "covers scenes 0..2, never the scene being entered")
	assert.Len(t, p.Sources, 3)
	for _, s := range p.Sources {
		for _, id := range s.SentenceIDs {
			assert.LessOrEqual(t, id, 2, "no not-yet-narrated source")
		}
	}
	// Display scene is the 4th (index 3), window matches its canonical timing.
	assert.Equal(t, int64(60_000_000), p.WindowStartUS)
	assert.Equal(t, int64(80_000_000), p.WindowEndUS)
}

func TestRecapWiringShortRunNoPlacement(t *testing.T) {
	assert.Empty(t, recapPlacementsForScenes(recapBriefs5()[:2]), "short run → no recap")
	assert.Empty(t, recapPlacementsForScenes(nil))
}

func TestRecapWiringPlanValidation(t *testing.T) {
	placements := recapPlacementsForScenes(recapBriefs5())
	require.NotEmpty(t, placements)
	plan, err := capabilityoverlay.BuildPlan(capabilityoverlay.PlanInput{
		PlanID: "recap-wire", VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: []capabilityoverlay.SceneInput{{ID: "s1"}, {ID: "s2"}, {ID: "s3"}, {ID: "s4"}, {ID: "s5"}},
		Recap:  placements,
	}, capabilityoverlay.PlannerConfig{})
	require.NoError(t, err, "recap items pass plan validation")
	var recaps []capabilityoverlay.OverlayItem
	for _, it := range plan.Items {
		if it.Kind == "quote" {
			if v, ok := it.Params["recap"]; ok && v == true {
				recaps = append(recaps, it)
			}
		}
	}
	require.NotEmpty(t, recaps, "recap cards reach the compiled plan")
	assert.Equal(t, len(placements), plan.RecapPlacements, "attempted placements observed")
	assert.Equal(t, len(recaps), plan.RecapCards, "admitted recap cards observed")
	for _, it := range recaps {
		assert.Equal(t, "QUOTE", it.TemplateID)
		assert.NotEmpty(t, it.PresetID)
	}
}

func TestRecapMetricsCountCardsPreservedInFusedChain(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{{
		SceneID: "display-scene",
		Params: map[string]any{"chain_segments": []capabilityoverlay.ChainSegment{
			{Params: map[string]any{"recap": true}},
			{Params: map[string]any{"recap": true}},
			{Params: map[string]any{"chart_card": true}},
		}},
	}}
	assert.Equal(t, 2, countRecapItems(items))
	assert.Equal(t, 1, countChartItems(items))
	assert.Equal(t, map[string]int{"display-scene": 2}, recapItemsByScene(items))
}
