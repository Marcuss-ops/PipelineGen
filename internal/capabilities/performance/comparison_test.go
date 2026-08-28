package performance

import "testing"

func TestRunComparisonCarriesPersistedAnalytics(t *testing.T) {
	c := RunComparison{RunID: "run-1", JobID: "job-1", WorkerSlotCount: 2, Concurrency: 1.8, CPUAvgPct: 74, CPUPeakPct: 96, RSSPeakBytes: 1234, GPUAvgPct: 61, GPUPeakPct: 88, RTF: .32, CacheRatio: .5, ScalingEfficiency: .9}
	if c.RTF != .32 || c.CacheRatio != .5 || c.ScalingEfficiency != .9 {
		t.Fatalf("comparison=%+v", c)
	}
}
