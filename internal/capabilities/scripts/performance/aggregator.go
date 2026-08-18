package performance

import (
	"context"
	"fmt"
	"math"
	"sort"

	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// ReportSource loads the canonical inputs for one job. It is the read-only
// seam the aggregator depends on; production wiring supplies an adapter that
// reads the persisted RunReport and its audio-operation projection, plus the
// recorded execution steps. No implementation in this package performs I/O.
type ReportSource interface {
	Load(ctx context.Context, jobID string) (kernobs.RunReport, scriptgeneration.AudioPipelineMetrics, []scriptgeneration.ExecutionStep, error)
}

// Aggregator is the read-only performance aggregator. It projects canonical
// inputs into PerformanceReport and AggregatePerformanceReport views and never
// writes or estimates timings.
type Aggregator struct {
	source   ReportSource
	resolver PhaseResolver
}

// NewAggregator builds an aggregator over a report source. A nil resolver
// falls back to the canonical DefaultPhaseResolver.
func NewAggregator(source ReportSource, resolver PhaseResolver) *Aggregator {
	if resolver == nil {
		resolver = DefaultPhaseResolver{}
	}
	return &Aggregator{source: source, resolver: resolver}
}

// BuildJobReport loads one job and projects it into a PerformanceReport.
func (a *Aggregator) BuildJobReport(ctx context.Context, jobID string) (PerformanceReport, error) {
	if a == nil || a.source == nil {
		return PerformanceReport{}, fmt.Errorf("performance: aggregator has no report source")
	}
	run, audio, steps, err := a.source.Load(ctx, jobID)
	if err != nil {
		return PerformanceReport{}, fmt.Errorf("performance: load job %s: %w", jobID, err)
	}
	return Build(run, audio, steps, a.resolver), nil
}

// Compare loads and projects each job, then aggregates them in JobIDs order.
func (a *Aggregator) Compare(ctx context.Context, jobIDs []string) (AggregatePerformanceReport, error) {
	reports := make([]PerformanceReport, 0, len(jobIDs))
	for _, id := range jobIDs {
		report, err := a.BuildJobReport(ctx, id)
		if err != nil {
			return AggregatePerformanceReport{}, err
		}
		reports = append(reports, report)
	}
	return Aggregate(reports), nil
}

// Build projects one job's canonical inputs into a PerformanceReport using the
// given resolver.
func Build(run kernobs.RunReport, audio scriptgeneration.AudioPipelineMetrics, steps []scriptgeneration.ExecutionStep, resolver PhaseResolver) PerformanceReport {
	phases := resolver.Resolve(run, audio, steps)
	return PerformanceReport{
		JobID:      run.JobID,
		WallTimeMS: run.WallTimeMs,
		Script:     scriptSummary(run, steps),
		Phases:     phases,
		Unmeasured: unmeasuredPhases(phases),
		Waits:      waitSummary(run),
		Audio:      audioSummary(audio),
	}
}

// unmeasuredPhases lists, in canonical order, the phases whose canonical source
// was absent for this job. It is the explicit "missing" signal of the report:
// nothing is estimated and each gap is named together with the source that
// should have populated it.
func unmeasuredPhases(phases []PhaseMeasurement) []UnmeasuredPhase {
	var out []UnmeasuredPhase
	for _, m := range phases {
		if !m.Measured {
			out = append(out, UnmeasuredPhase{Phase: m.Phase, Source: m.Source})
		}
	}
	return out
}

// Aggregate computes the cross-job phase statistics. Only measured phase
// durations contribute to the stats; unmeasured phases shrink MeasuredJobs but
// never fabricate a value.
func Aggregate(reports []PerformanceReport) AggregatePerformanceReport {
	out := AggregatePerformanceReport{JobIDs: make([]string, 0, len(reports))}
	wallValues := make([]int64, 0, len(reports))
	byPhase := make(map[PerformancePhase][]int64, len(Phases()))
	for _, r := range reports {
		out.JobIDs = append(out.JobIDs, r.JobID)
		wallValues = append(wallValues, r.WallTimeMS)
		for _, m := range r.Phases {
			if m.Measured {
				byPhase[m.Phase] = append(byPhase[m.Phase], m.DurationMS)
			}
		}
	}

	wallAvg := avgOf(wallValues)
	out.Wall = phaseStats("", wallValues, 0)
	for _, p := range Phases() {
		st := phaseStats(p, byPhase[p], wallAvg)
		out.Phases = append(out.Phases, st)
		if st.MeasuredJobs == 0 {
			out.Unmeasured = append(out.Unmeasured, p)
		}
	}
	return out
}

func phaseStats(phase PerformancePhase, values []int64, wallAvg float64) PhaseStats {
	if len(values) == 0 {
		return PhaseStats{Phase: phase}
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	st := PhaseStats{
		Phase:        phase,
		MeasuredJobs: len(values),
		MinMS:        sorted[0],
		MedianMS:     percentile(sorted, 50),
		AvgMS:        avgOf(values),
		P95MS:        percentile(sorted, 95),
		MaxMS:        sorted[len(sorted)-1],
	}
	if wallAvg > 0 {
		st.PctWall = st.AvgMS / wallAvg * 100
	}
	return st
}

// percentile returns the nearest-rank percentile of a sorted slice.
func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p/100.0*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

func avgOf(values []int64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum int64
	for _, v := range values {
		sum += v
	}
	return float64(sum) / float64(len(values))
}
