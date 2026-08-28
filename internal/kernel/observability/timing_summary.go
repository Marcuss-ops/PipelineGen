package observability

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// TimingSummary is the canonical timing-diagnostic projection of a completed
// run. It is derived from the persisted Stages / Operations / WallTimeMs on
// the run's own clock — never a second timer. It is the single shape exposed
// by timing diagnostics (e.g. /api/jobs/:id/full) so operators can answer
// per-phase questions (wall, Qdrant, SQLite, plan, LLM, NLP, TTS, Rust,
// persistence, docs) without recomputing anything by hand.
//
// The breakdown fields (attributed / unattributed / bottleneck) are computed
// from TOP-LEVEL stages only, so nested stages are never double-counted into
// wall attribution. The stages / operations lists are exhaustive (including
// nested boundaries) so diagnostics can still surface `persistence.sqlite`
// and `document.publish` even though they are nested under their processor
// stage.
type TimingSummary struct {
	// WallMs and ExecutionWallMs are the same attempt-owned wall measurement;
	// the latter is the canonical name for new consumers.
	WallMs          int64 `json:"wall_ms"`
	ExecutionWallMs int64 `json:"execution_wall_ms"`
	// QueueWaitMs is the attempt queue wait (enqueue → processing start),
	// measured by the runtime at claim. It is the canonical per-job answer to
	// "how long did this clip sit in the queue before a worker picked it up".
	QueueWaitMs int64 `json:"queue_wait_ms,omitempty"`
	// AttributedMs is the sum of top-level (exclusive/sequential) stage
	// wall times.
	AttributedMs int64 `json:"attributed_ms"`
	// UnattributedMs is wall_ms - attributed_ms (>= 0). A large value means
	// the pipeline is missing stage instrumentation.
	UnattributedMs int64 `json:"unattributed_ms"`
	// UnattributedPercent is UnattributedMs as a percentage of wall_ms.
	UnattributedPercent float64 `json:"unattributed_percent"`
	OverlappedMs        int64   `json:"overlapped_ms"`
	// BottleneckStage is the top-level stage with the largest wall time.
	BottleneckStage string `json:"bottleneck_stage,omitempty"`
	// BottleneckOperation is "component.operation" of the dominant operation
	// recorded directly under BottleneckStage, or "" when absent.
	BottleneckOperation string `json:"bottleneck_operation,omitempty"`
	// BottleneckPercent is the bottleneck stage's wall time as a percentage of
	// wall_ms.
	BottleneckPercent float64 `json:"bottleneck_percent,omitempty"`
	// CriticalPath is the ordered chain of top-level (sequential) stages,
	// ordered by wall-time start, each with its wall time and share of wall_ms.
	CriticalPath []CriticalPathStage `json:"critical_path,omitempty"`
	// Stages is the exhaustive per-stage wall-time list (nested stages
	// included), aggregated by name and sorted by name.
	Stages []TimingStage `json:"stages,omitempty"`
	// Operations is the exhaustive per-"component.operation" work list with
	// call counts, sorted by component then operation. WorkMs is accumulated
	// (summed) work and MUST NOT be reported as pipeline wall time.
	Operations []TimingOperation `json:"operations,omitempty"`
	// Fanout separates stage wall time from summed parallel work for fan-out
	// boundaries (see FanoutReports).
	Fanout []FanoutReport `json:"fanout,omitempty"`
}

// TimingStage is one named stage and its wall duration.
type TimingStage struct {
	Name       string `json:"name"`
	DurationMs int64  `json:"duration_ms"`
}

// TimingOperation is one technical boundary and its accumulated work.
type TimingOperation struct {
	Component string `json:"component"`
	Operation string `json:"operation"`
	// Calls is the number of times the boundary was measured.
	Calls int64 `json:"calls"`
	// WorkMs is the accumulated (summed) duration of all calls. For parallel
	// fan-out it can exceed the enclosing stage wall time and must never be
	// reported as wall time.
	WorkMs int64 `json:"work_ms"`
	// QueueWaitMs is the accumulated queue wait across all calls.
	QueueWaitMs int64 `json:"queue_wait_ms,omitempty"`
	// Metadata is the merge of the per-call metadata_json facts (e.g. the
	// Ollama split: input/output tokens, model_load_ms, inference_wall_ms,
	// inference_work_ms, cold_start). Numeric values are summed; strings that
	// are identical across calls are kept, otherwise replaced by "mixed".
	Metadata map[string]any `json:"metadata,omitempty"`
}

// TimingSummary returns the canonical diagnostic projection. It is pure and
// deterministic: the same report always yields the same summary.
func (r *RunReport) TimingSummary() TimingSummary {
	if r == nil {
		return TimingSummary{}
	}
	return r.timingSummaryWithWall(nonNegative(r.WallTimeMs))
}

// timingSummaryWithWall is the wall-parameterized core of TimingSummary so
// callers with a live clock (a still-running Run using ElapsedMs) can derive
// the same summary before WallTimeMs is finalized.
func (r *RunReport) timingSummaryWithWall(wallMs int64) TimingSummary {
	bd := r.breakdownWithWall(wallMs)
	return TimingSummary{
		WallMs:              wallMs,
		ExecutionWallMs:     wallMs,
		QueueWaitMs:         nonNegative(r.QueueWaitMs),
		AttributedMs:        bd.AttributedStageMs,
		UnattributedMs:      bd.UnattributedMs,
		UnattributedPercent: bd.UnattributedPercent,
		OverlappedMs:        bd.OverlappedMs,
		BottleneckStage:     bd.BottleneckStage,
		BottleneckOperation: bd.BottleneckOperation,
		BottleneckPercent:   bd.BottleneckPercent,
		CriticalPath:        bd.CriticalPath,
		Stages:              timingStages(r.Stages),
		Operations:          timingOperations(r.Operations),
		Fanout:              r.FanoutReports(),
	}
}

// FormatCriticalPath renders the critical path as a single compact,
// operator-friendly line: "stage_a(3.5%) > stage_b(91.1%) > ...".
// Percentages carry one decimal so log output stays stable and grep-able.
// It returns "" for a report with no critical path.
func (s TimingSummary) FormatCriticalPath() string {
	if len(s.CriticalPath) == 0 {
		return ""
	}
	var b strings.Builder
	for i, st := range s.CriticalPath {
		if i > 0 {
			b.WriteString(" > ")
		}
		b.WriteString(st.Name)
		b.WriteByte('(')
		b.WriteString(fmt.Sprintf("%.1f%%", st.Percent))
		b.WriteByte(')')
	}
	return b.String()
}

// timingStages aggregates stages by name (summing durations for repeated
// same-name stages, which are sequential and therefore additive) and sorts
// by name for a deterministic wire shape.
func timingStages(stages []StageReport) []TimingStage {
	if len(stages) == 0 {
		return nil
	}
	byName := make(map[string]int64, len(stages))
	for _, st := range stages {
		byName[st.Name] += nonNegative(st.DurationMs)
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]TimingStage, 0, len(names))
	for _, name := range names {
		out = append(out, TimingStage{Name: name, DurationMs: byName[name]})
	}
	return out
}

// timingOperations aggregates operations by (component, operation), counting
// calls and accumulating work. Output is sorted by component then operation.
func timingOperations(ops []OperationReport) []TimingOperation {
	if len(ops) == 0 {
		return nil
	}
	type key struct{ component, operation string }
	byKey := make(map[key]*TimingOperation, len(ops))
	var keys []key
	seen := make(map[key]bool, len(ops))
	for _, op := range ops {
		k := key{op.Component, op.Operation}
		a := byKey[k]
		if a == nil {
			a = &TimingOperation{Component: op.Component, Operation: op.Operation}
			byKey[k] = a
		}
		a.Calls++
		a.WorkMs += nonNegative(op.DurationMs)
		a.QueueWaitMs += nonNegative(op.QueueWaitMs)
		mergeOperationMetadata(a, op.MetadataJSON)
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].component != keys[j].component {
			return keys[i].component < keys[j].component
		}
		return keys[i].operation < keys[j].operation
	})
	out := make([]TimingOperation, 0, len(keys))
	for _, k := range keys {
		out = append(out, *byKey[k])
	}
	return out
}

// mergeOperationMetadata merges one operation's metadata_json into the
// aggregate. Numeric values are summed so per-call counters (tokens,
// durations) accumulate across the fan-out; booleans are counted as
// cold_start counts; strings must be identical across calls or become
// "mixed".
func mergeOperationMetadata(a *TimingOperation, metadataJSON string) {
	if a == nil || metadataJSON == "" {
		return
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &meta); err != nil || len(meta) == 0 {
		return
	}
	if a.Metadata == nil {
		a.Metadata = make(map[string]any, len(meta))
	}
	for key, value := range meta {
		prev, exists := a.Metadata[key]
		if !exists {
			// Normalize booleans to 0/1 floats on first insert so later
			// merges can accumulate them (e.g. cold_start counts).
			if b, ok := value.(bool); ok {
				a.Metadata[key] = boolToFloat(b)
			} else {
				a.Metadata[key] = value
			}
			continue
		}
		switch v := value.(type) {
		case float64:
			if p, ok := prev.(float64); ok {
				a.Metadata[key] = p + v
			}
		case bool:
			if p, ok := prev.(float64); ok {
				a.Metadata[key] = p + boolToFloat(v)
			} else if p, ok := prev.(bool); ok {
				a.Metadata[key] = boolToFloat(p) + boolToFloat(v)
			} else {
				a.Metadata[key] = boolToFloat(v)
			}
		case string:
			if p, ok := prev.(string); ok && p == v {
				// keep the uniform value
			} else {
				a.Metadata[key] = "mixed"
			}
		}
	}
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
