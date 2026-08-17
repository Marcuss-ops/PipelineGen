package performance

import (
	"fmt"
	"strings"
	"text/tabwriter"
)

// RenderText returns a human-readable comparison table of the aggregate
// report: one row per phase with min/median/avg/p95/max, the percentage of
// wall time, and the number of jobs that actually measured the phase. It is a
// pure projection of the same PhaseStats the JSON carries — it never invents
// numbers, and unmeasured phases render as "-" rather than a fabricated 0.
func RenderText(r AggregatePerformanceReport) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "phase\tmin\tmedian\tavg\tp95\tmax\t% wall\tjobs")

	rows := make([]PhaseStats, 0, len(r.Phases)+1)
	rows = append(rows, r.Wall)
	rows = append(rows, r.Phases...)
	for _, s := range rows {
		label := string(s.Phase)
		if label == "" {
			label = "wall"
		}
		if s.MeasuredJobs == 0 {
			fmt.Fprintf(w, "%s\t-\t-\t-\t-\t-\t-\t0\n", label)
			continue
		}
		pct := s.PctWall
		if label == "wall" {
			pct = 100
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\n",
			label,
			formatMS(float64(s.MinMS)),
			formatMS(float64(s.MedianMS)),
			formatMS(s.AvgMS),
			formatMS(float64(s.P95MS)),
			formatMS(float64(s.MaxMS)),
			formatPct(pct),
			s.MeasuredJobs,
		)
	}
	_ = w.Flush()

	if len(r.Unmeasured) > 0 {
		names := make([]string, 0, len(r.Unmeasured))
		for _, p := range r.Unmeasured {
			names = append(names, string(p))
		}
		fmt.Fprintf(&b, "\nunmeasured: %s\n", strings.Join(names, ", "))
	}
	return b.String()
}

// formatMS renders a millisecond value as either whole milliseconds (sub-second)
// or seconds with two decimals (≥1s), so a 31ms plan compile and an 18.36s
// inference stay readable side by side.
func formatMS(ms float64) string {
	if ms < 1000 {
		return fmt.Sprintf("%.0fms", ms)
	}
	return fmt.Sprintf("%.2fs", ms/1000)
}

func formatPct(p float64) string {
	return fmt.Sprintf("%.1f%%", p)
}
