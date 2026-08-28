package performance

import "context"

type RunComparison struct {
	RunID             string  `json:"run_id"`
	JobID             string  `json:"job_id"`
	BatchID           string  `json:"batch_id,omitempty"`
	WorkerSlotCount   int     `json:"worker_slot_count"`
	WallMS            int64   `json:"wall_ms"`
	Concurrency       float64 `json:"concurrency"`
	CPUAvgPct         float64 `json:"cpu_avg_pct"`
	CPUPeakPct        float64 `json:"cpu_peak_pct"`
	RSSPeakBytes      int64   `json:"rss_peak_bytes"`
	GPUAvgPct         float64 `json:"gpu_avg_pct"`
	GPUPeakPct        float64 `json:"gpu_peak_pct"`
	RTF               float64 `json:"rtf"`
	CacheRatio        float64 `json:"cache_ratio"`
	ScalingEfficiency float64 `json:"scaling_efficiency"`
}

type RunComparisonReader interface {
	CompareRuns(context.Context, []string) ([]RunComparison, error)
}
