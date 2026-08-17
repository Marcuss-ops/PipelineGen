package observability

import "sort"

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
	// AttributedStageMs is the sum of top-level (exclusive/sequential) stage
	// wall times.
	AttributedStageMs int64
	// UnattributedMs is total_wall_ms - attributed_stage_ms. A large value
	// means the pipeline is missing stage instrumentation.
	UnattributedMs int64
	// UnattributedPercent is UnattributedMs as a percentage of total wall time.
	UnattributedPercent float64
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
	attributed := int64(0)
	for _, st := range top {
		attributed += nonNegative(st.DurationMs)
	}
	unattributed := int64(0)
	if wall > attributed {
		unattributed = wall - attributed
	}
	b := Breakdown{
		AttributedStageMs: attributed,
		UnattributedMs:    unattributed,
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
			if !other.StartedAt.After(st.StartedAt) && !other.FinishedAt.Before(st.FinishedAt) {
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
