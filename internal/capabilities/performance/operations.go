// Package performance — operations.go owns the per-operation measurement
// contract: ONE canonical shape for every media operation (probe, normalize,
// cut, watermark, render, mux, ...) and ONE durable sink port. It is the
// "ObservedExecutor" contract: the execution layer measures each operation
// in a single point and records one OperationMeasurement, never scattered
// across operation handlers.
//
// The measurement is the payload BEFORE persistence: run_id/job_id/step_id
// are identity added by the recorder when it persists (performance_operations
// table). CPU time and frames are measured in the process that owns the work
// (the Rust media executor reports its child FFmpeg consumption); Go
// measures wall time and input/output bytes at the boundary.
package performance

import "context"

// OperationMeasurement is one media operation's measured facts. Zero fields
// mean "not measurable at this boundary" (e.g. source SHA256 is filled by
// the caller when known, not hashed at the choke point).
type OperationMeasurement struct {
	Operation        string
	SourceSHA256     string
	SourceDurationMS int64
	SourceSizeBytes  int64

	Width  int
	Height int
	FPS    float64

	InputCodec  string
	OutputCodec string

	ElapsedMS       int64
	CPUUserMS       int64
	CPUSystemMS     int64
	OutputSizeBytes int64

	CacheHit bool
	Strategy string

	MetadataJSON string
	CreatedAt    string
}

// OperationRecorder is the single durable sink for per-operation media
// measurements. Implementations persist into the canonical performance
// registry (performance_operations); the sink must never fail the operation
// it measures — a metric write failure is a logged warning, not a render
// failure. Correlation identity (run_id/job_id/step_id) is resolved from
// the execution context by the implementation.
type OperationRecorder interface {
	RecordOperation(ctx context.Context, measurement OperationMeasurement) error
}

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
