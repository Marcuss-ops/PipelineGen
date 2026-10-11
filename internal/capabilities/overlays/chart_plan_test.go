package overlays

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseChartValueStrict(t *testing.T) {
	for text, want := range map[string]float64{
		"42%": 42, "18": 18, "1.5": 1.5, "1,5": 1.5,
		"€100": 100, "100€": 100, "$75": 75, "-3.2%": -3.2,
		"  60 % ": 60,
	} {
		got, ok := parseChartValue(text)
		require.True(t, ok, "verbatim %q must parse", text)
		assert.Equal(t, want, got, "verbatim %q", text)
	}
	for _, text := range []string{"", "tredici", "abc123", "12ab", "1.2.3", "--4", "%", "€", "molti", "42 euro"} {
		_, ok := parseChartValue(text)
		assert.False(t, ok, "%q must never coerce a chart value", text)
	}
}

func chartNumbers(texts ...string) []TimedAnnotation {
	out := make([]TimedAnnotation, 0, len(texts))
	for i, text := range texts {
		start := int64(i) * 10_000_000
		out = append(out, TimedAnnotation{
			Text: text, Type: "PERCENT",
			StartMs: start / 1000, EndMs: (start + 8_000_000) / 1000,
			StartUS: start, DurationUS: 8_000_000, Score: 0.9,
		})
	}
	return out
}

func TestChartPlacementsForScenesComparison(t *testing.T) {
	got := ChartPlacementsForScenes([]SceneInput{{ID: "s1", Numbers: chartNumbers("42%", "18%")}})
	require.Len(t, got, 1, "two grounded numbers → one chart")
	assert.Equal(t, "s1", got[0].SceneID)
	assert.Equal(t, []float64{42, 18}, got[0].Values)
	assert.Equal(t, []string{"42%", "18%"}, got[0].Labels)
	assert.Equal(t, int64(0), got[0].WindowStartUS)
	assert.Equal(t, int64(18_000_000), got[0].WindowEndUS)
}

func TestChartPlacementsForScenesNoComparison(t *testing.T) {
	assert.Empty(t, ChartPlacementsForScenes([]SceneInput{{ID: "s", Numbers: chartNumbers("42%")}}), "single number → no chart")
	assert.Empty(t, ChartPlacementsForScenes([]SceneInput{{ID: "s", Numbers: chartNumbers("tredici", "molti")}}), "word numbers → no chart, never coerced")
	assert.Empty(t, ChartPlacementsForScenes([]SceneInput{{ID: "", Numbers: chartNumbers("1", "2")}}), "empty scene → no chart")
	tiny := []TimedAnnotation{{
		Text: "1", Type: "NUMBER", StartMs: 0, EndMs: 500,
		StartUS: 0, DurationUS: 500_000, Score: 0.9,
	}, {
		Text: "2", Type: "NUMBER", StartMs: 500, EndMs: 1000,
		StartUS: 500_000, DurationUS: 500_000, Score: 0.9,
	}}
	assert.Empty(t, ChartPlacementsForScenes([]SceneInput{{ID: "s", Numbers: tiny}}), "sub-2s window → no chart")
}

func TestChartPlacementsCapsAtSix(t *testing.T) {
	got := ChartPlacementsForScenes([]SceneInput{{ID: "s", Numbers: chartNumbers("1", "2", "3", "4", "5", "6", "7", "8")}})
	require.Len(t, got, 1)
	assert.Len(t, got[0].Values, ChartMaxValues, "chart data stays readable")
}

func TestBuildChartItemsShapeContract(t *testing.T) {
	got := BuildChartItems("plan-chart", []ChartPlacement{{
		SceneID: "s1", WindowStartUS: 0, WindowEndUS: 18_000_000,
		Values: []float64{42, 18}, Labels: []string{"42%", "18%"},
	}}, 1920, 1080)
	require.Len(t, got, 1)
	it := got[0]
	assert.Equal(t, "s1", it.SceneID)
	assert.Equal(t, "shape", it.Kind)
	assert.Equal(t, "SHAPE", it.TemplateID)
	assert.Empty(t, it.PresetID, "SHAPE is preset-less")
	assert.Equal(t, "chart", it.Params["shape"])
	chart, ok := it.Params["chart"].(map[string]any)
	require.True(t, ok, "chart payload must be an object")
	assert.Equal(t, "bar", chart["chart_type"])
	assert.Equal(t, []float64{42, 18}, chart["data"])
	assert.Equal(t, true, it.Params["chart_card"])
	assert.Equal(t, int64(0), it.StartMs)
	assert.Equal(t, int64(18000), it.EndMs)
	assert.Equal(t, it.StartMs, it.StartUS/1000, "ms projection floors start_us")
	assert.Equal(t, it.EndMs, (it.StartUS+it.DurationUS+999)/1000, "ms projection ceils end_us")
	again := BuildChartItems("plan-chart", []ChartPlacement{{
		SceneID: "s1", WindowStartUS: 0, WindowEndUS: 18_000_000,
		Values: []float64{42, 18}, Labels: []string{"42%", "18%"},
	}}, 1920, 1080)
	assert.Equal(t, got, again, "deterministic")
}

func TestBuildChartItemsSkipsInvalid(t *testing.T) {
	assert.Empty(t, BuildChartItems("p", []ChartPlacement{{
		SceneID: "s", WindowStartUS: 0, WindowEndUS: 5_000_000,
		Values: []float64{7}, Labels: []string{"7"},
	}}, 1920, 1080), "single value → no chart")
	assert.Empty(t, BuildChartItems("p", []ChartPlacement{{
		SceneID: "", WindowStartUS: 0, WindowEndUS: 5_000_000,
		Values: []float64{1, 2}, Labels: []string{"1", "2"},
	}}, 1920, 1080), "empty scene → no chart")
}

func TestChartPlanValidationFailClosed(t *testing.T) {
	plan, err := BuildPlan(PlanInput{
		PlanID: "chart-invalid", VideoID: "v", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: []SceneInput{{ID: "s"}},
		Charts: []ChartPlacement{{
			SceneID: "s", WindowStartUS: 0, WindowEndUS: 5_000_000,
			Values: []float64{1}, Labels: []string{"1"},
		}},
	}, PlannerConfig{})
	require.NoError(t, err, "invalid placement is skipped at build, never validated")
	assert.Equal(t, 0, plan.ChartCards)
	bad := OverlayPlan{
		SchemaVersion: SchemaVersionPlan, PlanID: "p", VideoID: "v",
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Items: []OverlayItem{{
			ID: "c1", SceneID: "s", Kind: "shape", TemplateID: "SHAPE",
			StartMs: 0, EndMs: 5000, StartUS: 0, DurationUS: 5_000_000,
			Params: map[string]any{"shape": "chart", "chart": map[string]any{"chart_type": "candles", "data": []any{1.0, 2.0}}},
		}},
	}
	require.Error(t, bad.Validate(), "unsupported chart_type fails closed")
	bad.Items[0].Params["chart"] = map[string]any{"chart_type": "bar", "data": []any{1.0}}
	require.Error(t, bad.Validate(), "single-value data fails closed")
}

func TestChartEmissionBudgetGate(t *testing.T) {
	numbers := chartNumbers("42%", "18%")
	phrases := make([]TimedAnnotation, 0, 3)
	for i, text := range []string{"La crisi morde i consumi interni", "Il governo annuncia nuove misure urgenti", "Le imprese chiedono sostegno al credito"} {
		phrases = append(phrases, TimedAnnotation{
			Text: text, StartMs: int64(i) * 1000, EndMs: int64(i)*1000 + 18000,
			StartUS: int64(i) * 1_000_000, DurationUS: 18_000_000, Score: 0.9,
		})
	}
	crowded, err := BuildPlan(PlanInput{
		PlanID: "chart-crowded", VideoID: "v", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: []SceneInput{{ID: "s", Numbers: numbers, Phrases: phrases}},
		Charts: ChartPlacementsForScenes([]SceneInput{{ID: "s", Numbers: numbers}}),
	}, PlannerConfig{MaxNumbers: 5, MaxPhrases: 5})
	require.NoError(t, err)
	assert.Equal(t, 1, crowded.ChartPlacements)
	assert.Equal(t, 0, crowded.ChartCards, "crowded window: chart skipped at emission, dense scene stays readable")
	free, err := BuildPlan(PlanInput{
		PlanID: "chart-free", VideoID: "v", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: []SceneInput{{ID: "s", Numbers: numbers}},
		Charts: ChartPlacementsForScenes([]SceneInput{{ID: "s", Numbers: numbers}}),
	}, PlannerConfig{})
	require.NoError(t, err)
	require.Equal(t, 1, free.ChartCards, "free window: chart reaches the compiled plan")
	var chart *OverlayItem
	for i := range free.Items {
		if v, ok := free.Items[i].Params["chart_card"]; ok && v == true {
			chart = &free.Items[i]
		}
	}
	require.NotNil(t, chart, "chart card reaches the compiled plan")
	assert.Equal(t, "SHAPE", chart.TemplateID)
}
