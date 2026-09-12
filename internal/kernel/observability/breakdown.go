package observability

import (
	"sort"
	"time"
)

// CriticalPathStage is one sequential stage on the run's critical path — the
// ordered chain of top-level (non-nested) stages. The chain is ordered by
// wall-time start, so it reflects the actual execution sequence (never a
// name sort) and lets operators read the dominant serial chain directly.
type CriticalPathStage struct {
	// Name is the stage name.
	Name string `json:"name"`
	// DurationMs is the stage's wall time.
	DurationMs int64 `json:"duration_ms"`
	// Percent is the stage's share of total run wall time.
	Percent float64 `json:"percent"`
}

// Breakdown is the canonical per-run timing breakdown derived from a
// RunReport's TOP-LEVEL stage wall times — never from nested-duration sums.
//
// A stage is top-level when its wall-time interval is not strictly contained
// within any other stage's interval. This mirrors the script.generate
// hierarchy (script.prepare → normalize/validate/source.resolve/plan,
// script.postprocess → processors → persistence.sqlite/document.publish) so
// each wall-clock millisecond is attributed to exactly one sequential phase.
type Breakdown struct {
	// AttributedStageMs is the WALL TIME covered by the top-level
	// (exclusive/sequential) stages — the union of their intervals, never the
	// sum of their durations. Summing durations is only equal to the union when
	// the stages are strictly sequential; under concurrency (the streaming
	// script pipeline overlaps generate/translate/TTS/render) a sum can exceed
	// the run wall and would inflate the attributed budget instead of showing
	// the overlap. Union keeps this value wall-bounded by construction.
	AttributedStageMs int64
	// UnattributedMs is total_wall_ms - measured_coverage_ms. A large value
	// means the pipeline is missing stage instrumentation.
	UnattributedMs int64
	// UnattributedPercent is UnattributedMs as a percentage of total wall time.
	UnattributedPercent float64
	// OverlappedMs is the top-level phase WORK that ran concurrently (the sum
	// of top-level stage durations minus the wall time they actually covered).
	// It is zero for a strictly sequential pipeline and grows exactly by the
	// work that overlapped another phase, so a run can never hide concurrency
	// behind an attributed_stage_ms larger than total_wall_ms.
	OverlappedMs int64
	// BottleneckStage is the name of the top-level stage with the largest wall
	// time.
	BottleneckStage string
	// BottleneckOperation is "component.operation" of the dominant operation
	// recorded directly under BottleneckStage, or "" when that stage has no
	// direct operation (its work is further nested).
	BottleneckOperation string
	// BottleneckPercent is BottleneckStage's wall time as a percentage of
	// total run wall time.
	BottleneckPercent float64
	// CriticalPath is the ordered chain of top-level (sequential) stages,
	// ordered by wall-time start. Each entry carries its wall time and its
	// percentage of total run wall time.
	CriticalPath []CriticalPathStage
}

// Breakdown computes the canonical timing breakdown for the report. It is
// pure and deterministic: the same Stages/Operations/WallTimeMs always yield
// the same Breakdown.
func (r *RunReport) Breakdown() Breakdown {
	if r == nil {
		return Breakdown{}
	}
	return r.breakdownWithWall(nonNegative(r.WallTimeMs))
}

// breakdownWithWall is the wall-parameterized core of Breakdown so callers
// with a live clock (a still-running Run using ElapsedMs) can derive the
// same breakdown before WallTimeMs is finalized.
func (r *RunReport) breakdownWithWall(wall int64) Breakdown {
	top := topLevelStages(r.Stages)
	// Attributed wall time is the UNION of the top-level intervals, not the sum
	// of their durations: overlapping phases must not inflate it beyond the run
	// wall. The summed work is reported separately as OverlappedMs.
	attributed := topLevelWallMs(r, top, wall)
	sumTop := int64(0)
	for _, st := range top {
		sumTop += nonNegative(st.DurationMs)
	}
	covered := measuredCoverageMs(r, wall)
	unattributed := wall - covered
	if unattributed < 0 {
		unattributed = 0
	}
	// The concurrency excess: phase work measured above the wall time those
	// same phases covered. Zero for a strictly sequential pipeline.
	overlapped := sumTop - attributed
	if overlapped < 0 {
		overlapped = 0
	}
	b := Breakdown{
		AttributedStageMs: attributed,
		UnattributedMs:    unattributed,
		OverlappedMs:      overlapped,
		CriticalPath:      criticalPathStages(top, wall),
	}
	if wall > 0 {
		b.UnattributedPercent = float64(unattributed) / float64(wall) * 100
	}
	if len(top) > 0 {
		best := top[0]
		for _, st := range top[1:] {
			if st.DurationMs > best.DurationMs {
				best = st
			}
		}
		b.BottleneckStage = best.Name
		b.BottleneckOperation = dominantOperation(r.Operations, best.Name)
		if wall > 0 {
			b.BottleneckPercent = float64(best.DurationMs) / float64(wall) * 100
		}
	}
	return b
}

// wallInterval is a half-open [start,end] window in milliseconds relative to
// the run's StartedAt anchor, clamped to the run wall.
type wallInterval struct{ start, end int64 }

// wallIntervalFor converts a start/finish pair into a wall-anchored interval.
// It reports ok=false when the pair is unusable (zero, inverted, or entirely
// outside the run wall) so callers can fall back to a declared duration.
func wallIntervalFor(start, finish, base time.Time, wall int64) (wallInterval, bool) {
	if start.IsZero() || finish.IsZero() || !finish.After(start) {
		return wallInterval{}, false
	}
	s := start.Sub(base).Milliseconds()
	e := finish.Sub(base).Milliseconds()
	if e <= 0 || s >= wall {
		return wallInterval{}, false
	}
	if s < 0 {
		s = 0
	}
	if e > wall {
		e = wall
	}
	if e <= s {
		return wallInterval{}, false
	}
	return wallInterval{s, e}, true
}

// unionWallMs returns the measure of the union of the intervals. Summing them
// would double-count overlapping work; the union is the wall time they cover.
func unionWallMs(intervals []wallInterval) int64 {
	if len(intervals) == 0 {
		return 0
	}
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].start == intervals[j].start {
			return intervals[i].end < intervals[j].end
		}
		return intervals[i].start < intervals[j].start
	})
	covered := int64(0)
	start, end := intervals[0].start, intervals[0].end
	for _, next := range intervals[1:] {
		if next.start > end {
			covered += end - start
			start, end = next.start, next.end
			continue
		}
		if next.end > end {
			end = next.end
		}
	}
	return covered + end - start
}

// topLevelWallMs returns the wall time covered by the union of the top-level
// stage intervals — the attributed (sequential) budget. Stages without a usable
// anchor contribute their declared duration instead, because an interval that
// cannot be placed on the wall cannot be unioned. The result is always
// wall-bounded, so attributed_ms can never exceed total_wall_ms.
func topLevelWallMs(r *RunReport, top []StageReport, wall int64) int64 {
	if r == nil || wall <= 0 || len(top) == 0 {
		return 0
	}
	base := r.StartedAt
	if base.IsZero() {
		sum := int64(0)
		for _, st := range top {
			sum += nonNegative(st.DurationMs)
		}
		if sum > wall {
			return wall
		}
		return sum
	}
	intervals := make([]wallInterval, 0, len(top))
	unanchored := int64(0)
	for _, st := range top {
		if iv, ok := wallIntervalFor(st.StartedAt, st.FinishedAt, base, wall); ok {
			intervals = append(intervals, iv)
			continue
		}
		unanchored += nonNegative(st.DurationMs)
	}
	covered := unionWallMs(intervals) + unanchored
	if covered > wall {
		return wall
	}
	return covered
}

// measuredCoverageMs computes the union of all anchored stage and operation
// intervals. Summing them would double-count concurrency; taking their union
// tells us how much wall time has an owner at all. This is deliberately kept
// separate from the critical-path union used for AttributedStageMs.
func measuredCoverageMs(r *RunReport, wall int64) int64 {
	if r == nil || wall <= 0 {
		return 0
	}
	base := r.StartedAt
	if base.IsZero() {
		return attributedFallback(r, wall)
	}
	intervals := make([]wallInterval, 0, len(r.Stages)+len(r.Operations))
	add := func(start, finish time.Time) {
		if iv, ok := wallIntervalFor(start, finish, base, wall); ok {
			intervals = append(intervals, iv)
		}
	}
	for _, stage := range r.Stages {
		add(stage.StartedAt, stage.FinishedAt)
	}
	for _, operation := range r.Operations {
		add(operation.StartedAt, operation.FinishedAt)
	}
	if len(intervals) == 0 {
		return attributedFallback(r, wall)
	}
	return unionWallMs(intervals)
}

func attributedFallback(r *RunReport, wall int64) int64 {
	if r == nil {
		return 0
	}
	covered := int64(0)
	for _, stage := range topLevelStages(r.Stages) {
		covered += nonNegative(stage.DurationMs)
	}
	if covered > wall {
		return wall
	}
	return covered
}

// criticalPathStages projects the top-level stages onto the ordered critical
// path. Stages are ordered by wall-time start; stages without a start anchor
// keep their input order and sort after anchored ones so legacy unanchored
// stages remain represented deterministically.
func criticalPathStages(top []StageReport, wall int64) []CriticalPathStage {
	if len(top) == 0 {
		return nil
	}
	ordered := append([]StageReport(nil), top...)
	sort.SliceStable(ordered, func(i, j int) bool {
		si, sj := ordered[i].StartedAt, ordered[j].StartedAt
		if si.IsZero() != sj.IsZero() {
			return !si.IsZero()
		}
		if si.IsZero() {
			return false // stable: preserve input order for unanchored stages
		}
		if si.Equal(sj) {
			return ordered[i].FinishedAt.Before(ordered[j].FinishedAt)
		}
		return si.Before(sj)
	})
	out := make([]CriticalPathStage, 0, len(ordered))
	for _, st := range ordered {
		cp := CriticalPathStage{Name: st.Name, DurationMs: nonNegative(st.DurationMs)}
		if wall > 0 {
			cp.Percent = float64(st.DurationMs) / float64(wall) * 100
		}
		out = append(out, cp)
	}
	return out
}

// topLevelStages returns the stages whose wall-time interval is not strictly
// contained within any other stage's interval. Stages without a usable
// StartedAt/FinishedAt interval are treated as top-level (a stage cannot be
// nested without an anchor).
func topLevelStages(stages []StageReport) []StageReport {
	if len(stages) == 0 {
		return nil
	}
	out := make([]StageReport, 0, len(stages))
	for i := range stages {
		st := stages[i]
		if st.StartedAt.IsZero() || st.FinishedAt.IsZero() {
			out = append(out, st)
			continue
		}
		contained := false
		for j := range stages {
			if i == j {
				continue
			}
			other := stages[j]
			if other.StartedAt.IsZero() || other.FinishedAt.IsZero() {
				continue
			}
			if !other.StartedAt.After(st.StartedAt) && !other.FinishedAt.Before(st.FinishedAt) &&
				(other.StartedAt.Before(st.StartedAt) || other.FinishedAt.After(st.FinishedAt)) {
				contained = true
				break
			}
		}
		if !contained {
			out = append(out, st)
		}
	}
	return out
}

// dominantOperation returns "component.operation" for the operation with the
// largest duration recorded directly under the given stage, or "" when the
// stage has no direct operation.
func dominantOperation(ops []OperationReport, stage string) string {
	best := ""
	bestMs := int64(-1)
	for _, op := range ops {
		if op.Stage != stage {
			continue
		}
		if op.DurationMs > bestMs {
			bestMs = op.DurationMs
			if op.Component == "" {
				best = op.Operation
			} else {
				best = op.Component + "." + op.Operation
			}
		}
	}
	return best
}
