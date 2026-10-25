// golden_summary_set.go owns the golden-chain aggregate: the median-of-N-runs
// projection that the overlay-throughput measurement contract requires
// ("stesso payload immutabile, mediana 3 run, item validati only" —
// TICKET-OVERLAY-THROUGHPUT-100-MIN). One run's summary cannot answer the
// acceptance question; the reported number is the MEDIAN across the golden
// legs of one chain, quoted next to its range and the per-run mix.
//
// Same purity rule as golden_summary.go: nothing here touches a database or
// a file. The CLI (cmd/goldensummary --aggregate) owns loading the per-run
// summaries; the median definition stays testable in isolation.
package rendermetrics

import "sort"

// GoldenSummarySetSchema identifies the aggregate artifact.
const GoldenSummarySetSchema = "pipelinegen.golden-overlay-summary-set.v1"

// SetRun is the per-run projection the summary set aggregates. Every field
// is copied from one run's v1 summary; nothing is recomputed here.
type SetRun struct {
	JobID               string  `json:"job_id"`
	Status              string  `json:"status"`
	OverlayPlanItems    int     `json:"overlay_plan_items"`
	WindowSeconds       float64 `json:"window_seconds"`
	ItemsPerMin         float64 `json:"items_per_min"`
	PerItemServiceAvgMS int64   `json:"per_item_render_service_avg_ms"`
	PerItemServiceP50MS int64   `json:"per_item_render_service_p50_ms"`
	TotalServiceMS      int64   `json:"total_service_ms"`
}

// SetMedian is the median across the runs, one entry per contract metric.
// Medians follow the ServiceStat.P50 convention: middle value for an odd
// count, average of the two middle values for an even count (int metrics
// round to nearest).
type SetMedian struct {
	OverlayPlanItems    int     `json:"overlay_plan_items"`
	WindowSeconds       float64 `json:"window_seconds"`
	ItemsPerMin         float64 `json:"items_per_min"`
	PerItemServiceAvgMS int64   `json:"per_item_render_service_avg_ms"`
	PerItemServiceP50MS int64   `json:"per_item_render_service_p50_ms"`
	TotalServiceMS      int64   `json:"total_service_ms"`
}

// SetRange quotes the observed span of the headline throughput so the median
// is never reported alone (README convention: "78,9 item/min (range
// 74,6–86,2)").
type SetRange struct {
	ItemsPerMinMin float64 `json:"items_per_min_min"`
	ItemsPerMinMax float64 `json:"items_per_min_max"`
}

// SummarySet is the golden-chain aggregate artifact.
type SummarySet struct {
	Schema string    `json:"schema"`
	Runs   []SetRun  `json:"runs"`
	Median SetMedian `json:"median"`
	Range  SetRange  `json:"range"`
}

// BuildGoldenSummarySet projects N runs into the median set. An empty run
// list yields a set with zero medians and no range — never a fabricated
// estimate (godlike/07 fail-closed).
func BuildGoldenSummarySet(runs []SetRun) SummarySet {
	set := SummarySet{Schema: GoldenSummarySetSchema, Runs: runs}
	if len(runs) == 0 {
		return set
	}

	planItems := make([]int64, 0, len(runs))
	window := make([]float64, 0, len(runs))
	perMin := make([]float64, 0, len(runs))
	avgMS := make([]int64, 0, len(runs))
	p50MS := make([]int64, 0, len(runs))
	totalMS := make([]int64, 0, len(runs))
	for _, r := range runs {
		planItems = append(planItems, int64(r.OverlayPlanItems))
		window = append(window, r.WindowSeconds)
		perMin = append(perMin, r.ItemsPerMin)
		avgMS = append(avgMS, r.PerItemServiceAvgMS)
		p50MS = append(p50MS, r.PerItemServiceP50MS)
		totalMS = append(totalMS, r.TotalServiceMS)
	}

	set.Median = SetMedian{
		OverlayPlanItems:    int(medianInts(planItems)),
		WindowSeconds:       medianFloats(window),
		ItemsPerMin:         medianFloats(perMin),
		PerItemServiceAvgMS: medianInts(avgMS),
		PerItemServiceP50MS: medianInts(p50MS),
		TotalServiceMS:      medianInts(totalMS),
	}

	sortedPerMin := append([]float64(nil), perMin...)
	sort.Float64s(sortedPerMin)
	set.Range = SetRange{
		ItemsPerMinMin: sortedPerMin[0],
		ItemsPerMinMax: sortedPerMin[len(sortedPerMin)-1],
	}
	return set
}

// medianInts mirrors ServiceStat.P50: middle value for an odd count, average
// of the two middle values (rounded to nearest) for an even count.
func medianInts(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return int64(float64(sorted[mid-1]+sorted[mid]) / 2)
}

// medianFloats mirrors medianInts for float metrics.
func medianFloats(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}
