// golden_summary.go owns the golden-run per-item-class throughput summary.
//
// The overlay throughput golden case (ops/jobs/overlay_throughput_golden_100_20261022)
// historically produced ad-hoc summaries whose single throughput number
// ("item/min") hid the class mix: a map-heavy plan spends 20–90 s per item
// while a phrase item costs 2–5 s, so one aggregate service average cannot
// compare two runs with different mixes. The canonical metric is therefore
// PER CLASS: the worker-measured render service (`render_ms`, the same field
// the run summaries called `per_item_render_service_ms`) grouped by the
// semantic overlay-plan item kind, plus the derived window capacity.
//
// This package is pure computation over already-loaded samples: the CLI
// (cmd/goldensummary) owns the job.full.json / SQLite loading. Nothing here
// touches a database, so the metric definition stays testable in isolation.
package rendermetrics

import (
	"math"
	"sort"
)

// ItemSample is one rendered overlay item joined against its plan class.
// Class is the semantic overlay-plan item kind (map, text_phrase, image,
// entity_image, number, ...); empty means the item could not be joined to a
// plan entry and is counted under "unknown" rather than dropped.
type ItemSample struct {
	ItemID    string
	Class     string
	Status    string // overlay_render status: COMPLETED | FAILED | ...
	RenderMS  int64  // worker-measured Chronon render service (per_item_render_service_ms)
	EncodeMS  int64  // worker-measured encode service
	QueuedMS  int64  // cumulative queue wait observed for the item (0 = unmeasured)
	StartedAt int64  // unix millis when the item entered the render queue (0 = unknown)
	EndedAt   int64  // unix millis when the item finished (0 = unknown)
}

// ServiceStat is the sample distribution of one service-time population.
// P50 is the median (average of the two middle values for an even count),
// rounded to the nearest millisecond. A population with no samples reports
// Count 0 and zero values — never a fabricated estimate.
type ServiceStat struct {
	Count int   `json:"count"`
	AvgMS int64 `json:"avg_ms"`
	P50MS int64 `json:"p50_ms"`
	MinMS int64 `json:"min_ms"`
	MaxMS int64 `json:"max_ms"`
}

// ClassStat is the per-class projection of the golden run. ServiceSharePct
// is the class's share of the summed render service across all items — the
// number that shows where a window actually went.
type ClassStat struct {
	Items           int         `json:"items"`
	Completed       int         `json:"completed"`
	ServiceMS       ServiceStat `json:"service_ms"`
	ServiceSharePct float64     `json:"service_share_pct"`
}

// Summary is the canonical golden-run projection. WindowSeconds and
// ItemsPerMin describe the observed render window (first item queued to last
// item ended); ItemsPerMin is derived from the window and must never be read
// without the per-class block beside it.
type Summary struct {
	Items        int                  `json:"items"`
	Completed    int                  `json:"completed"`
	StatusCounts map[string]int       `json:"status_counts,omitempty"`
	ClassCounts  map[string]int       `json:"class_counts"`
	PerClass     map[string]ClassStat `json:"per_class"`
	// PerItemServiceMS is the aggregate over ALL classes. It exists for
	// continuity with the historical run summaries; per-class comparisons use
	// PerClass.
	PerItemServiceMS ServiceStat `json:"per_item_service_ms"`
	TotalServiceMS   int64       `json:"total_service_ms"`
	WindowSeconds    float64     `json:"window_seconds,omitempty"`
	ItemsPerMin      float64     `json:"items_per_min,omitempty"`
	UnknownClass     int         `json:"unknown_class,omitempty"`
}

const unknownClass = "unknown"

// BuildGoldenSummary projects joined item samples into the canonical
// per-class summary. Samples without a class are reported under "unknown";
// samples are never silently dropped from the aggregate.
func BuildGoldenSummary(samples []ItemSample) Summary {
	summary := Summary{
		StatusCounts: map[string]int{},
		ClassCounts:  map[string]int{},
		PerClass:     map[string]ClassStat{},
	}
	perClassMS := map[string][]int64{}
	perClassDone := map[string]int{}
	var allMS []int64
	for _, s := range samples {
		class := s.Class
		if class == "" {
			class = unknownClass
			summary.UnknownClass++
		}
		summary.Items++
		summary.ClassCounts[class]++
		if s.Status != "" {
			summary.StatusCounts[s.Status]++
		}
		if s.Status == "COMPLETED" {
			summary.Completed++
			perClassDone[class]++
		}
		if s.RenderMS > 0 {
			allMS = append(allMS, s.RenderMS)
			perClassMS[class] = append(perClassMS[class], s.RenderMS)
		}
	}
	summary.PerItemServiceMS = serviceStat(allMS)
	summary.TotalServiceMS = sum(allMS)
	for class, items := range summary.ClassCounts {
		ms := perClassMS[class]
		stat := ClassStat{
			Items:     items,
			Completed: perClassDone[class],
			ServiceMS: serviceStat(ms),
		}
		if summary.TotalServiceMS > 0 {
			stat.ServiceSharePct = math.Round(
				float64(sum(ms))/float64(summary.TotalServiceMS)*1000) / 10
		}
		summary.PerClass[class] = stat
	}
	summary.WindowSeconds, summary.ItemsPerMin = windowOf(samples)
	return summary
}

// windowOf derives the observed render window from item timestamps: first
// queued → last ended. Items without both timestamps contribute nothing to
// the window; with fewer than two timestamped items the window is not
// estimable and both values stay zero (absent, never fabricated).
func windowOf(samples []ItemSample) (seconds float64, itemsPerMin float64) {
	var minStart, maxEnd int64
	timestamped := 0
	for _, s := range samples {
		if s.StartedAt <= 0 || s.EndedAt <= 0 || s.EndedAt < s.StartedAt {
			continue
		}
		timestamped++
		if minStart == 0 || s.StartedAt < minStart {
			minStart = s.StartedAt
		}
		if s.EndedAt > maxEnd {
			maxEnd = s.EndedAt
		}
	}
	if timestamped < 2 || maxEnd <= minStart {
		return 0, 0
	}
	seconds = float64(maxEnd-minStart) / 1000
	// Capacity of the observed window: timestamped items per minute. Rounded
	// to one decimal like the historical summaries.
	itemsPerMin = math.Round(float64(timestamped)/seconds*600) / 10
	return seconds, itemsPerMin
}

func serviceStat(values []int64) ServiceStat {
	if len(values) == 0 {
		return ServiceStat{}
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	stat := ServiceStat{
		Count: len(sorted),
		MinMS: sorted[0],
		MaxMS: sorted[len(sorted)-1],
		AvgMS: int64(math.Round(float64(sum(sorted)) / float64(len(sorted)))),
	}
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		stat.P50MS = sorted[mid]
	} else {
		stat.P50MS = int64(math.Round(float64(sorted[mid-1]+sorted[mid]) / 2))
	}
	return stat
}

func sum(values []int64) int64 {
	var total int64
	for _, v := range values {
		total += v
	}
	return total
}
