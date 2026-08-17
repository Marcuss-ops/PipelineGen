package performance

import (
	"strings"
	"testing"
)

func TestRenderTextShowsAggregateStats(t *testing.T) {
	reports := []PerformanceReport{
		reportWithWall("a", 1000, 100),
		reportWithWall("b", 2000, 300),
		reportWithWall("c", 3000, 500),
	}
	out := RenderText(Aggregate(reports))

	for _, want := range []string{
		// Header row.
		"phase", "min", "median", "avg", "p95", "max", "% wall", "jobs",
		// Wall row (denominator rendered at 100%).
		"wall", "1.00s", "3.00s", "100.0%",
		// The single measured phase (rust_mix): 100/300/500 ms → 15% of wall.
		"rust_mix", "100ms", "300ms", "500ms", "15.0%",
		// Unmeasured phases are named, not fabricated.
		"unmeasured:", "script_gemma",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered table missing %q:\n%s", want, out)
		}
	}
}

func TestRenderTextRendersUnmeasuredPhasesAsDash(t *testing.T) {
	// No job measures anything: every phase row must render "-" and the wall
	// row must still print (one job with wall time).
	out := RenderText(Aggregate([]PerformanceReport{
		reportWithWall("a", 1000, 0),
	}))

	// The wall row is measured (1 job), so it prints a value.
	if !strings.Contains(out, "wall") {
		t.Fatalf("wall row missing:\n%s", out)
	}
	// Every phase is unmeasured and must appear as a dash, not "0ms".
	if strings.Contains(out, "0ms") {
		t.Errorf("unmeasured phases must render as '-', found 0ms:\n%s", out)
	}
	foundDashRow := false
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "script_gemma") {
			if !strings.Contains(line, "-") {
				t.Errorf("script_gemma row should render dashes: %q", line)
			}
			foundDashRow = true
		}
	}
	if !foundDashRow {
		t.Fatalf("script_gemma row missing:\n%s", out)
	}
}

func TestFormatMSUnits(t *testing.T) {
	cases := []struct {
		ms   float64
		want string
	}{
		{0, "0ms"},
		{31, "31ms"},
		{999, "999ms"},
		{1000, "1.00s"},
		{18430, "18.43s"},
	}
	for _, c := range cases {
		if got := formatMS(c.ms); got != c.want {
			t.Errorf("formatMS(%v) = %q, want %q", c.ms, got, c.want)
		}
	}
	if got := formatPct(15); got != "15.0%" {
		t.Errorf("formatPct(15) = %q, want %q", got, "15.0%")
	}
}
