// Package performance — operations.go owns the per-operation measurement
// contract: ONE canonical shape for every media operation (probe, normalize,
// cut, watermark, render, mux, ...) and ONE durable read-model sink port. The
// execution layer measures each operation in a single point, promotes it to
// kernel OperationReport, and never scatters timing across operation handlers.
//
// The report is the payload BEFORE persistence: run_id/job_id/step_id
// are identity added by the recorder when it persists (performance_operations
// table). CPU time and frames are measured in the process that owns the work
// (the Rust media executor reports its child FFmpeg consumption); Go
// measures wall time and input/output bytes at the boundary.
package performance

import (
	"context"
)

// OperationStats is one operation's aggregate across the recorded runs. It
// is the query-side answer to "what does this operation cost": runs,
// average elapsed/output and cache hits. AvgRTF is the Real-Time Factor
// (average elapsed / average source duration; < 1 means faster than
// realtime) — derived in the projection, never stored.
type OperationStats struct {
	Operation           string
	Runs                int64
	AvgElapsedMS        float64
	AvgOutputSizeBytes  float64
	CacheHits           int64
	AvgSourceDurationMS float64
	AvgRTF              float64
}

// OperationAnalytics is the query-side port over performance_operations,
// consumed by dashboards and the benchmark comparison. An empty since is
// "all recorded operations".
type OperationAnalytics interface {
	OperationStats(ctx context.Context, since string) ([]OperationStats, error)
}

// BenchmarkStats is the benchmark projection of one operation's canonical
// performance_operations rows. Every value is derived from the canonical
// elapsed_ms / source_duration_ms columns written by ObservedExecutor; it is
// never re-measured at the read boundary. MedianRTF is the median-based
// Real-Time Factor (median elapsed / median source duration; < 1 means faster
// than realtime) — more robust to outliers than the average-based AvgRTF.
type BenchmarkStats struct {
	Operation       string
	Samples         int64
	MedianElapsedMS float64
	MedianSourceMS  float64
	MedianRTF       float64
	CacheHits       int64
}

// OperationSampleSource returns canonical elapsed_ms samples for one
// operation, oldest first — the benchmark baseline derived from
// performance_operations (never re-measured at the read boundary).
type OperationSampleSource interface {
	OperationSamples(ctx context.Context, operation, since string) ([]int64, error)
}

// BenchmarkSource is the query-side benchmark port over performance_operations.
// It returns canonical elapsed samples and per-operation aggregates so a
// benchmark regression compares canonical durations instead of re-measuring
// wall time at the benchmark boundary. An empty since is "all recorded
// operations".
type BenchmarkSource interface {
	OperationSampleSource
	BenchmarkStats(ctx context.Context, since string) ([]BenchmarkStats, error)
}
