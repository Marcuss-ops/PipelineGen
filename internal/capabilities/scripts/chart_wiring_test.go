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
	assert.Equal(t, "SHAPE", chart.TemplateID)
	assert.Equal(t, "chart", chart.Params["shape"])
	chartPayload, ok := chart.Params["chart"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "bar", chartPayload["chart_type"])
	assert.Equal(t, []float64{42, 18}, chartPayload["data"])
	assert.Equal(t, countChartItems(plan.Items), plan.ChartCards, "sealed recount matches planner count")
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
