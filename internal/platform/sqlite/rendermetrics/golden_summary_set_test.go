package rendermetrics

import "testing"

// TestBuildGoldenSummarySet_MedianOfThree pins the measurement contract:
// the reported number is the median across the golden legs, quoted next to
// the observed range, with per-run rows preserved verbatim.
func TestBuildGoldenSummarySet_MedianOfThree(t *testing.T) {
	runs := []SetRun{
		{JobID: "job-r3", Status: "SUCCEEDED", OverlayPlanItems: 76, WindowSeconds: 58.7, ItemsPerMin: 86.2, PerItemServiceAvgMS: 1860, PerItemServiceP50MS: 1500, TotalServiceMS: 240000},
		{JobID: "job-r1", Status: "SUCCEEDED", OverlayPlanItems: 73, WindowSeconds: 55.0, ItemsPerMin: 74.6, PerItemServiceAvgMS: 2310, PerItemServiceP50MS: 1900, TotalServiceMS: 300000},
		{JobID: "job-r2", Status: "SUCCEEDED", OverlayPlanItems: 75, WindowSeconds: 52.9, ItemsPerMin: 78.9, PerItemServiceAvgMS: 2000, PerItemServiceP50MS: 1700, TotalServiceMS: 260000},
	}
	set := BuildGoldenSummarySet(runs)

	if set.Schema != GoldenSummarySetSchema {
		t.Fatalf("schema = %q, want %q", set.Schema, GoldenSummarySetSchema)
	}
	if len(set.Runs) != 3 || set.Runs[0].JobID != "job-r3" {
		t.Fatalf("runs must be preserved verbatim in input order: %+v", set.Runs)
	}

	if set.Median.OverlayPlanItems != 75 {
		t.Errorf("median plan items = %d, want 75", set.Median.OverlayPlanItems)
	}
	if set.Median.WindowSeconds != 55.0 {
		t.Errorf("median window = %v, want 55.0", set.Median.WindowSeconds)
	}
	if set.Median.ItemsPerMin != 78.9 {
		t.Errorf("median items/min = %v, want 78.9", set.Median.ItemsPerMin)
	}
	if set.Median.PerItemServiceAvgMS != 2000 {
		t.Errorf("median per-item avg = %d, want 2000", set.Median.PerItemServiceAvgMS)
	}
	if set.Median.PerItemServiceP50MS != 1700 {
		t.Errorf("median per-item p50 = %d, want 1700", set.Median.PerItemServiceP50MS)
	}
	if set.Median.TotalServiceMS != 260000 {
		t.Errorf("median total service = %d, want 260000", set.Median.TotalServiceMS)
	}

	if set.Range.ItemsPerMinMin != 74.6 || set.Range.ItemsPerMinMax != 86.2 {
		t.Errorf("range = %v–%v, want 74.6–86.2", set.Range.ItemsPerMinMin, set.Range.ItemsPerMinMax)
	}
}

// TestBuildGoldenSummarySet_EvenCountAveragesMiddleValues mirrors the
// ServiceStat.P50 convention for an even number of runs.
func TestBuildGoldenSummarySet_EvenCountAveragesMiddleValues(t *testing.T) {
	set := BuildGoldenSummarySet([]SetRun{
		{OverlayPlanItems: 70, ItemsPerMin: 70.0, WindowSeconds: 60.0},
		{OverlayPlanItems: 80, ItemsPerMin: 80.0, WindowSeconds: 50.0},
	})
	if set.Median.OverlayPlanItems != 75 {
		t.Errorf("median plan items = %d, want 75 (average of the two middle values)", set.Median.OverlayPlanItems)
	}
	if set.Median.ItemsPerMin != 75.0 {
		t.Errorf("median items/min = %v, want 75.0", set.Median.ItemsPerMin)
	}
	if set.Median.WindowSeconds != 55.0 {
		t.Errorf("median window = %v, want 55.0", set.Median.WindowSeconds)
	}
}

// TestBuildGoldenSummarySet_NoRunsIsFailClosed: no samples means zero
// medians and an empty range, never a fabricated estimate.
func TestBuildGoldenSummarySet_NoRunsIsFailClosed(t *testing.T) {
	set := BuildGoldenSummarySet(nil)
	if set.Schema != GoldenSummarySetSchema {
		t.Fatalf("schema = %q, want %q", set.Schema, GoldenSummarySetSchema)
	}
	if set.Median != (SetMedian{}) {
		t.Errorf("median = %+v, want zeros", set.Median)
	}
	if set.Range != (SetRange{}) {
		t.Errorf("range = %+v, want zeros", set.Range)
	}
	if len(set.Runs) != 0 {
		t.Errorf("runs = %d, want 0", len(set.Runs))
	}
}
