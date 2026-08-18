package observability

import "sort"

// OperationSummary is a read-only projection of canonical operation facts.
// It is intentionally computed from RunReport; callers must not maintain a
// second wall clock for the same fan-out.
type OperationSummary struct {
	Calls, TotalMs, QueueMs, WallMs int64
	MaxObserved                     int
}

func SummarizeOperations(report *RunReport, stage, operation string) OperationSummary {
	if report == nil {
		return OperationSummary{}
	}
	type interval struct{ start, end int64 }
	intervals := make([]interval, 0)
	var minStart, maxEnd int64
	var result OperationSummary
	for _, op := range report.Operations {
		if (stage != "" && op.Stage != stage) || (operation != "" && op.Operation != operation) {
			continue
		}
		result.Calls++
		result.TotalMs += op.DurationMs
		result.QueueMs += op.QueueWaitMs
		s, e := op.StartedAt.UnixMilli(), op.FinishedAt.UnixMilli()
		if s > 0 && e >= s {
			intervals = append(intervals, interval{s, e})
			if minStart == 0 || s < minStart {
				minStart = s
			}
			if e > maxEnd {
				maxEnd = e
			}
		}
	}
	if len(intervals) == 0 {
		result.WallMs = result.TotalMs
		return result
	}
	result.WallMs = maxEnd - minStart
	type boundary struct {
		at    int64
		delta int
	}
	bounds := make([]boundary, 0, len(intervals)*2)
	for _, in := range intervals {
		bounds = append(bounds, boundary{in.start, 1}, boundary{in.end, -1})
	}
	sort.Slice(bounds, func(i, j int) bool {
		if bounds[i].at == bounds[j].at {
			return bounds[i].delta < bounds[j].delta
		}
		return bounds[i].at < bounds[j].at
	})
	current := 0
	for _, b := range bounds {
		current += b.delta
		if current > result.MaxObserved {
			result.MaxObserved = current
		}
	}
	return result
}
