package observability

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
}

// Breakdown computes the canonical timing breakdown for the report. It is
// pure and deterministic: the same Stages/Operations/WallTimeMs always yield
// the same Breakdown.
func (r *RunReport) Breakdown() Breakdown {
	if r == nil {
		return Breakdown{}
	}
	top := topLevelStages(r.Stages)
	attributed := int64(0)
	for _, st := range top {
		attributed += nonNegative(st.DurationMs)
	}
	wall := nonNegative(r.WallTimeMs)
	unattributed := int64(0)
	if wall > attributed {
		unattributed = wall - attributed
	}
	b := Breakdown{
		AttributedStageMs: attributed,
		UnattributedMs:    unattributed,
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
	}
	return b
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
