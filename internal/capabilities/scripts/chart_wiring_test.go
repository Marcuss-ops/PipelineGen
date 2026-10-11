package scriptgeneration

// Live-wiring certification for bar charts: a strict grounded numeric
// comparison compiles through BuildPlan (emission budget gate + plan
// validation) like any other overlay item.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func chartWiringNumbers() []capabilityoverlay.TimedAnnotation {
	mk := func(text string, startUS int64) capabilityoverlay.TimedAnnotation {
		return capabilityoverlay.TimedAnnotation{
			Text: text, Type: "PERCENT",
			StartMs: startUS / 1000, EndMs: (startUS + 8_000_000 + 999) / 1000,
			StartUS: startUS, DurationUS: 8_000_000, Score: 0.9,
		}
	}
	return []capabilityoverlay.TimedAnnotation{mk("42%", 0), mk("18%", 10_000_000)}
}

func TestChartWiringComparisonReachesPlan(t *testing.T) {
	scenes := []capabilityoverlay.SceneInput{{ID: "s1", Numbers: chartWiringNumbers()}}
	placements := capabilityoverlay.ChartPlacementsForScenes(scenes)
	require.Len(t, placements, 1, "strict numeric comparison → one placement")
	plan, err := capabilityoverlay.BuildPlan(capabilityoverlay.PlanInput{
		PlanID: "chart-wire", VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: scenes, Charts: placements,
	}, capabilityoverlay.PlannerConfig{})
	require.NoError(t, err, "chart items pass plan validation")
	require.Equal(t, 1, plan.ChartCards, "chart cards reach the compiled plan")
	var chart *capabilityoverlay.OverlayItem
	for i := range plan.Items {
		if v, ok := plan.Items[i].Params["chart_card"]; ok && v == true {
			chart = &plan.Items[i]
		}
	}
	require.NotNil(t, chart)
	assert.Equal(t, "text_phrase", chart.Kind)
	assert.Equal(t, "IMPORTANT_PHRASE", chart.TemplateID)
	assert.Equal(t, "42%\n18%", chart.Text)
	assert.NotContains(t, chart.Params, "chart")
	bars := 0
	for _, item := range plan.Items {
		if item.Params["chart_bar"] == true {
			bars++
			assert.Equal(t, "SHAPE", item.TemplateID)
			assert.Equal(t, "rect", item.Params["shape"])
		}
	}
	assert.Equal(t, 2, bars)
	assert.Equal(t, 3, countChartItems(plan.Items), "sealed recount counts chart label and native bars")
	assert.Equal(t, 1, plan.ChartCards, "planner count tracks comparisons, not their native drawing primitives")
}

func TestChartWiringDisabledStaysEmpty(t *testing.T) {
	scenes := []capabilityoverlay.SceneInput{{ID: "s1", Numbers: chartWiringNumbers()}}
	plan, err := capabilityoverlay.BuildPlan(capabilityoverlay.PlanInput{
		PlanID: "chart-off", VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: scenes,
	}, capabilityoverlay.PlannerConfig{})
	require.NoError(t, err)
	assert.Equal(t, 0, plan.ChartCards, "no placements in → no charts out (kill-switch path)")
	assert.Equal(t, 0, countChartItems(plan.Items))
}
