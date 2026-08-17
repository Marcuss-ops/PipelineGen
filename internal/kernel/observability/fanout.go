package observability

import "sort"

// FanoutReport summarizes one parallel fan-out boundary. It separates the
// stage wall time (the sequential time the stage occupied the run) from the
// accumulated per-call work, so a consumer can see fan-out without ever
// mistaking summed parallel work for pipeline wall time.
type FanoutReport struct {
	// Stage is the operation Stage field the calls were recorded under.
	Stage string `json:"stage"`
	// WallMs is the wall time of the enclosing stage(s): the sequential time
	// those stages occupied. When a single stage wraps a parallel batch this
	// is the real batch wall time; when multiple same-name stages are
	// recorded sequentially, it is their sum.
	WallMs int64 `json:"wall_ms"`
	// Calls is the number of operations recorded under the stage.
	Calls int64 `json:"calls"`
	// WorkMs is the accumulated (summed) duration of all calls. For parallel
	// fan-out it can exceed WallMs and MUST NOT be reported as wall time.
	WorkMs int64 `json:"work_ms"`
	// MaxMs is the longest single call duration.
	MaxMs int64 `json:"max_ms"`
}

// FanoutReports groups the report's operations by their Stage field and joins
// each group with the wall time of the matching StageReport(s). Output is
// sorted by stage name for determinism. Operations without a Stage are
// ignored (they cannot be attributed to a fan-out boundary).
//
// Example: 10 parallel edge-tts calls (each ~4s) recorded under one
// "voiceover.generate" stage yields WallMs≈5s, Calls=10, WorkMs≈40s,
// MaxMs≈4.4s — the parallel work is never summed into the wall time.
func (r *RunReport) FanoutReports() []FanoutReport {
	if r == nil {
		return nil
	}
	wallByStage := make(map[string]int64, len(r.Stages))
	for _, st := range r.Stages {
		wallByStage[st.Name] += nonNegative(st.DurationMs)
	}

	type aggregate struct {
		calls int64
		work  int64
		max   int64
	}
	byStage := make(map[string]*aggregate)
	var order []string
	seen := make(map[string]bool)
	for _, op := range r.Operations {
		if op.Stage == "" {
			continue
		}
		a := byStage[op.Stage]
		if a == nil {
			a = &aggregate{}
			byStage[op.Stage] = a
		}
		a.calls++
		a.work += nonNegative(op.DurationMs)
		if op.DurationMs > a.max {
			a.max = op.DurationMs
		}
		if !seen[op.Stage] {
			seen[op.Stage] = true
			order = append(order, op.Stage)
		}
	}
	sort.Strings(order)

	out := make([]FanoutReport, 0, len(order))
	for _, stage := range order {
		a := byStage[stage]
		out = append(out, FanoutReport{
			Stage:  stage,
			WallMs: wallByStage[stage],
			Calls:  a.calls,
			WorkMs: a.work,
			MaxMs:  a.max,
		})
	}
	return out
}
