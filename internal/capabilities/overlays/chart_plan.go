package overlays

// Package overlays — chart_plan.go wires grounded numeric comparisons into
// runtime bar charts.
//
// A chart is a visual comparison of already-grounded number annotations
// ("42% vs 18%"): it rides the certified preset-less SHAPE primitive with
// params.shape=chart, a path RenderingGen already compiles
// (semantic_shape.go: bar/line/pie/donut, fail-closed on bad data), so no
// RenderingGen change is needed. Unknown templates still fail closed at
// the worker.
//
// Provenance and timing rules (fail-soft, never guessed):
//   - values come ONLY from the scene's own grounded Numbers (certified
//     speech timing + NLP entity text); parseChartValue accepts strict
//     verbatim digit strings (optional decimal comma/dot, trailing % or
//     currency sign) and skips everything else — Italian number words,
//     embedded digits ("abc123") and unbounded magnitudes are never
//     coerced, so a qualitative scene yields no chart rather than a
//     fabricated one;
//   - one chart per scene, only when 2..6 values parse (extras beyond 6
//     are dropped in time order; the chart plots exactly its data array);
//   - the chart window is the verbatim span of its plotted numbers
//     (first start → last end), minimum ChartMinWindowUS; anything
//     smaller stays empty (X10: no element outside timing);
//   - BuildPlan applies the emission budget gate: SHAPE is structural
//     (never counted/dropped by DegradeOverlaps), so a chart whose window
//     already carries budget-or-more content items is skipped at emission.
//     Charts therefore compete for screen space like any other content.
// Deterministic: same scenes → same placements → same items.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Chart value/window limits.
const (
	// ChartMinValues/ChartMaxValues bound one chart's data array: fewer
	// than 2 is not a comparison, more than 6 does not stay readable.
	ChartMinValues = 2
	ChartMaxValues = 6
	// ChartMinWindowUS is the smallest chart window worth rendering.
	ChartMinWindowUS int64 = 2_000_000
)

// ChartPlacement is one bar chart bound to a scene's numeric span.
type ChartPlacement struct {
	SceneID       string
	WindowStartUS int64
	WindowEndUS   int64
	Values        []float64
	Labels        []string
}

// parseChartValue parses a strict verbatim numeric label: optional
// whitespace, digits with one decimal separator (dot or comma), one
// optional trailing %/currency sign. Anything else is rejected so the
// chart can never invent or coerce a value.
func parseChartValue(text string) (float64, bool) {
	t := strings.TrimSpace(text)
	if t == "" {
		return 0, false
	}
	t = strings.Trim(t, "%€$£¥")
	t = strings.TrimSpace(t)
	if t == "" {
		return 0, false
	}
	t = strings.ReplaceAll(t, " ", "")
	if strings.Count(t, ",") == 1 && !strings.Contains(t, ".") {
		t = strings.ReplaceAll(t, ",", ".")
	} else {
		t = strings.ReplaceAll(t, ",", "")
	}
	if t == "" || t == "-" || t == "." || t == "-." {
		return 0, false
	}
	digits := 0
	dot := false
	for i, r := range t {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '.' && !dot && i > 0:
			dot = true
		case r == '-' && i == 0:
			// leading sign only
		default:
			return 0, false
		}
	}
	if digits == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(t, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// ChartPlacementsForScenes derives at most one chart placement per scene
// from its grounded Numbers, in scene order. Scenes without a strict
// numeric comparison yield nothing.
func ChartPlacementsForScenes(scenes []SceneInput) []ChartPlacement {
	var out []ChartPlacement
	for _, scene := range scenes {
		sceneID := strings.TrimSpace(scene.ID)
		if sceneID == "" {
			continue
		}
		type plotted struct {
			value float64
			label string
			start int64
			end   int64
		}
		var pts []plotted
		for _, n := range scene.Numbers {
			if strings.TrimSpace(n.Text) == "" || n.StartUS < 0 || n.DurationUS <= 0 {
				continue
			}
			v, ok := parseChartValue(n.Text)
			if !ok {
				continue
			}
			pts = append(pts, plotted{value: v, label: n.Text, start: n.StartUS, end: n.StartUS + n.DurationUS})
			if len(pts) >= ChartMaxValues {
				break
			}
		}
		if len(pts) < ChartMinValues {
			continue
		}
		start, end := pts[0].start, pts[0].end
		vals := make([]float64, 0, len(pts))
		labels := make([]string, 0, len(pts))
		for _, p := range pts {
			if p.start < start {
				start = p.start
			}
			if p.end > end {
				end = p.end
			}
			vals = append(vals, p.value)
			labels = append(labels, p.label)
		}
		if end <= start || end-start < ChartMinWindowUS {
			continue
		}
		out = append(out, ChartPlacement{SceneID: sceneID, WindowStartUS: start, WindowEndUS: end, Values: vals, Labels: labels})
	}
	return out
}

// BuildChartItems lowers chart placements to overlay items on the
// preset-less SHAPE primitive (params.shape=chart, bar). Placements that
// cannot contribute are skipped silently. Items carry a "chart_card"
// marker so plan observability can count survivors after budgeting.
func BuildChartItems(planID string, placements []ChartPlacement, canvasW, canvasH int) []OverlayItem {
	var out []OverlayItem
	usedIDs := map[string]struct{}{}
	for _, p := range placements {
		sceneID := strings.TrimSpace(p.SceneID)
		if sceneID == "" || p.WindowEndUS <= p.WindowStartUS {
			continue
		}
		if len(p.Values) < ChartMinValues || len(p.Values) > ChartMaxValues {
			continue
		}
		for _, v := range p.Values {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				goto skip
			}
		}
		{
			data := append([]float64(nil), p.Values...)
			startMS := p.WindowStartUS / 1000
			endMS := (p.WindowEndUS + 999) / 1000
			if endMS <= startMS {
				continue
			}
			id := itemID(sceneID, "chart", strings.Join(p.Labels, "-vs-"))
			if _, dup := usedIDs[id]; dup {
				continue
			}
			usedIDs[id] = struct{}{}
			params := map[string]any{
				"shape": "chart",
				"chart": map[string]any{"chart_type": "bar", "data": data},
			}
			if canvasW > 0 && canvasH > 0 {
				params["width"] = float64(canvasW) * 0.72
				params["height"] = float64(canvasH) * 0.40
			}
			params["chart_card"] = true
			out = append(out, OverlayItem{
				ID: id, SceneID: sceneID,
				Kind: "shape", TemplateID: "SHAPE",
				StartMs: startMS, EndMs: endMS, StartUS: p.WindowStartUS, DurationUS: p.WindowEndUS - p.WindowStartUS,
				Params: params,
			})
		}
	skip:
	}
	if out == nil {
		return []OverlayItem{}
	}
	return out
}

// validateChartItem fail-closes chart-shaped items whose payload the
// RenderingGen worker would reject (unsupported type, empty or
// non-finite data): such a plan must never reach the queue looking
// validated. Non-chart items pass untouched.
func validateChartItem(item OverlayItem) error {
	if strings.TrimSpace(item.TemplateID) != "SHAPE" {
		return nil
	}
	shape, _ := item.Params["shape"].(string)
	_, hasChart := item.Params["chart"]
	if shape != "chart" && !hasChart {
		return nil
	}
	chartMap, _ := item.Params["chart"].(map[string]any)
	if chartMap == nil {
		return fmt.Errorf("overlay plan: chart item %q requires a chart payload", item.ID)
	}
	if rawType, ok := chartMap["chart_type"]; ok {
		t, _ := rawType.(string)
		switch t {
		case "bar", "line", "pie", "donut":
		default:
			return fmt.Errorf("overlay plan: chart item %q has unsupported chart_type", item.ID)
		}
	}
	rawData, ok := chartMap["data"]
	if !ok {
		return fmt.Errorf("overlay plan: chart item %q requires non-empty data", item.ID)
	}
	var data []float64
	switch d := rawData.(type) {
	case []float64:
		data = d
	case []any:
		for _, v := range d {
			switch n := v.(type) {
			case float64:
				data = append(data, n)
			case int:
				data = append(data, float64(n))
			case int64:
				data = append(data, float64(n))
			default:
				return fmt.Errorf("overlay plan: chart item %q data values must be finite numbers", item.ID)
			}
		}
	default:
		return fmt.Errorf("overlay plan: chart item %q data must be an array", item.ID)
	}
	if len(data) < ChartMinValues || len(data) > ChartMaxValues {
		return fmt.Errorf("overlay plan: chart item %q data must carry %d..%d values", item.ID, ChartMinValues, ChartMaxValues)
	}
	for _, v := range data {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("overlay plan: chart item %q data values must be finite numbers", item.ID)
		}
	}
	return nil
}

// chartWindowCrowded reports whether budget-or-more content items already
// overlap the chart window: SHAPE bypasses DegradeOverlaps (structural),
// so the emission gate in BuildPlan uses this to keep charts competing
// for screen space like ordinary content.
func chartWindowCrowded(items []OverlayItem, startUS, endUS int64, budget int) bool {
	if budget <= 0 {
		budget = DefaultOverlapBudget
	}
	overlapping := 0
	for _, it := range items {
		if contentPriorityForTemplate(it.TemplateID) == PriorityStructural {
			continue
		}
		s, e := it.StartUS, it.StartUS+it.DurationUS
		if it.DurationUS <= 0 {
			s, e = it.StartMs*1000, it.EndMs*1000
		}
		if s < endUS && startUS < e {
			overlapping++
			if overlapping >= budget {
				return true
			}
		}
	}
	return false
}
