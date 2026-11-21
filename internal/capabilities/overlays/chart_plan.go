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
