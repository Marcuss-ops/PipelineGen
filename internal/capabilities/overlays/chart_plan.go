package overlays

// Package overlays — chart_plan.go wires grounded numeric comparisons into
// runtime bar charts.
//
// A chart is a visual comparison of already-grounded number annotations
// ("42% vs 18%"): it is lowered to one IMPORTANT_PHRASE text label and
// preset-less SHAPE rect bars. The unsupported shape.chart pseudo-shape and
// chart data payload are never emitted; worker support is ordinary text/rect.
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
func ParseChartValue(text string) (float64, bool) {
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
		numbers := scene.Numbers
		if scene.ChartNumbers != nil {
			numbers = scene.ChartNumbers
		}
		for _, n := range numbers {
			if strings.TrimSpace(n.Text) == "" || n.StartUS < 0 || n.DurationUS <= 0 {
				continue
			}
			v, ok := ParseChartValue(n.Text)
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

// BuildChartItems lowers each grounded comparison into a label text layer and
// native rectangular bars. Chronon RenderPlan V3 deliberately rejects
// shape.chart; each rectangle and text layer is a certified primitive and all
// layers retain the exact grounded comparison window. Invalid placements are
// skipped, never guessed.
func BuildChartItems(planID string, placements []ChartPlacement, canvasW, canvasH int) []OverlayItem {
	if canvasW <= 0 || canvasH <= 0 {
		return []OverlayItem{}
	}
	var out []OverlayItem
	usedIDs := map[string]struct{}{}
	for _, p := range placements {
		sceneID := strings.TrimSpace(p.SceneID)
		if sceneID == "" || p.WindowStartUS < 0 || p.WindowEndUS <= p.WindowStartUS ||
			len(p.Values) < ChartMinValues || len(p.Values) > ChartMaxValues || len(p.Labels) != len(p.Values) {
			continue
		}
		valid := true
		maxAbs := 0.0
		for i, value := range p.Values {
			if math.IsNaN(value) || math.IsInf(value, 0) || strings.TrimSpace(p.Labels[i]) == "" {
				valid = false
				break
			}
			if math.Abs(value) > maxAbs {
				maxAbs = math.Abs(value)
			}
		}
		if !valid {
			continue
		}
		startMS, endMS := p.WindowStartUS/1000, (p.WindowEndUS+999)/1000
		if endMS <= startMS {
			continue
		}
		chartKey := itemID(sceneID, "chart", strings.Join(p.Labels, "-vs-"))
		if _, dup := usedIDs[chartKey]; dup {
			continue
		}
		usedIDs[chartKey] = struct{}{}
		chartW := float64(canvasW) * 0.72
		chartH := float64(canvasH) * 0.42
		left := (float64(canvasW) - chartW) / 2
		top := (float64(canvasH) - chartH) / 2
		rowH := chartH / float64(len(p.Values))
		labelTexts := make([]string, len(p.Labels))
		for i, label := range p.Labels {
			labelTexts[i] = strings.TrimSpace(label)
		}
		labelItem := OverlayItem{
			ID: chartKey, SceneID: sceneID, Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
			// IMPORTANT_PHRASE is a preset-driven template: RenderingGen rejects a
			// label without a preset_id, so the shared label card carries the same
			// certified phrase preset every other phrase item does. The SHAPE bars
			// stay preset-less (structural primitives).
			PresetID: selectPhrasePreset(planID, sceneID, chartKey),
			Text:     strings.Join(labelTexts, "\n"),
			StartMs:  startMS, EndMs: endMS, StartUS: p.WindowStartUS, DurationUS: p.WindowEndUS - p.WindowStartUS,
			Params: map[string]any{
				"position_x": left + chartW*0.24, "position_y": top + chartH/2,
				"width": chartW * 0.44, "height": chartH,
				"font_size_px": math.Min(float64(canvasH)*0.042, 44), "fill": "#FFFFFF", "position": "center", "style": "headline",
				"chart_card": true, "chart_group_id": chartKey,
			},
		}
		out = append(out, labelItem)
		const maxBarWidthRatio = 0.44
		maxBarWidth := chartW * maxBarWidthRatio
		barHeight := math.Max(4, rowH*0.52)
		for i, value := range p.Values {
			fraction := 0.0
			if maxAbs > 0 {
				fraction = math.Abs(value) / maxAbs
			}
			barWidth := math.Max(4, maxBarWidth*fraction)
			barID := chartKey + fmt.Sprintf("__bar_%02d", i)
			// RenderingGen/Chronon positions shape layers by their center.
			// Keep every bar inside the right-hand plot column by placing its
			// center half its width after the plot's left edge, and center it
			// vertically within its row.
			barX := left + chartW*0.52 + barWidth/2
			barY := top + rowH*(float64(i)+0.5)
			out = append(out, OverlayItem{
				ID: barID, SceneID: sceneID, Kind: "shape", TemplateID: "SHAPE",
				StartMs: startMS, EndMs: endMS, StartUS: p.WindowStartUS, DurationUS: p.WindowEndUS - p.WindowStartUS,
				Params: map[string]any{
					"shape": "rect", "width": barWidth, "height": barHeight,
					"position": []float64{barX, barY}, "fill": []float64{0.20, 0.78, 0.94, 1}, "chart_bar": true, "chart_group_id": chartKey,
				},
			})
		}
	}
	if out == nil {
		return []OverlayItem{}
	}
	return out
}

// PruneOrphanChartBars keeps a comparison atomic after overlap or budget
// admission removes its label card. Rectangles without an admitted label are
// visual clutter and are therefore removed as a group.
func PruneOrphanChartBars(items []OverlayItem) []OverlayItem {
	admittedGroups := make(map[string]struct{})
	barCounts := make(map[string]int)
	for _, item := range items {
		if item.Params == nil {
			continue
		}
		group, _ := item.Params["chart_group_id"].(string)
		if group == "" {
			continue
		}
		if item.Params["chart_card"] == true {
			admittedGroups[group] = struct{}{}
		}
		if item.Params["chart_bar"] == true {
			barCounts[group]++
		}
	}
	out := make([]OverlayItem, 0, len(items))
	for _, item := range items {
		if item.Params != nil && item.Params["chart_card"] == true {
			group, _ := item.Params["chart_group_id"].(string)
			if _, admitted := admittedGroups[group]; !admitted || barCounts[group] == 0 {
				continue
			}
		}
		if item.Params != nil && item.Params["chart_bar"] == true {
			group, _ := item.Params["chart_group_id"].(string)
			if _, admitted := admittedGroups[group]; !admitted {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}

// validateChartItem prevents unsupported shape.chart and pseudo-chart payloads
// from passing plan validation and reaching the queue. Supported charts must
// already have been lowered to ordinary rect/text items by BuildChartItems.
func validateChartItem(item OverlayItem) error {
	if strings.TrimSpace(item.TemplateID) != "SHAPE" {
		return nil
	}
	shape, _ := item.Params["shape"].(string)
	if shape == "chart" {
		return fmt.Errorf("overlay plan: item %q uses unsupported shape.chart; lower charts to rect/text primitives", item.ID)
	}
	if _, hasChart := item.Params["chart"]; hasChart {
		return fmt.Errorf("overlay plan: item %q carries unsupported chart payload; lower charts to rect/text primitives", item.ID)
	}
	return nil
}

// chartWindowCrowded reports whether the chart window already has budget-or-
// more competing content items. A chart's bars are structural rects, while its
// single label card is the admitted content unit; this keeps the comparison
// atomic while allowing it to compete like ordinary content.
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
