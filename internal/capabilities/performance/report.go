package performance

import "context"

type PerformanceReport struct {
	JobID       string            `json:"job_id"`
	Job         JobReport         `json:"job"`
	Queue       QueueReport       `json:"queue"`
	Preparation PreparationReport `json:"preparation"`
	Render      RenderReport      `json:"render"`
	Resources   ResourceReport    `json:"resources"`
	Publication PublicationReport `json:"publication"`
	Derived     DerivedReport     `json:"derived"`
}

type JobReport struct {
	Type, Status, WorkerID, Host string
	StartedAt, CompletedAt       string
	WallTimeMS, QueueWaitMS      int64
}
type QueueReport struct {
	WaitMS int64
	Status string
}
type PreparationReport struct {
	StageMS map[string]int64
	TotalMS int64
}
type RenderReport struct {
	MetricsV2  map[string]any   `json:"metrics_v2,omitempty"`
	Operations []OperationStats `json:"operations,omitempty"`
	TotalMS    int64            `json:"total_ms"`
}
type ResourceReport struct {
	Samples int64 `json:"samples"`
}
type PublicationReport struct {
	TotalMS int64 `json:"total_ms"`
}
type DerivedReport struct {
	XRT                   float64 `json:"xrt"`
	SpeedFactor           float64 `json:"speed_factor"`
	CriticalPathPercent   float64 `json:"critical_path_percent"`
	ParallelismEfficiency float64 `json:"parallelism_efficiency"`
	ClipsPerMinute        float64 `json:"clips_per_minute"`
	CacheRatio            float64 `json:"cache_ratio"`
}

type PerformanceReportReader interface {
	PerformanceReport(context.Context, string) (PerformanceReport, error)
}

// PersistedBenchmarkMetrics exposes only read-side derived metrics to a
// benchmark/report consumer. It never owns measurement or persistence.
type PersistedBenchmarkMetrics struct {
	JobID   string        `json:"job_id"`
	Derived DerivedReport `json:"derived"`
}
