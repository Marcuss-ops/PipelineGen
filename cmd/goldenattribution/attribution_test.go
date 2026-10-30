package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSimulateMakespanFIFOAndLPT(t *testing.T) {
	// The big job is last in FIFO order, so one lane absorbs it after a small
	// job and the makespan is 12; LPT starts it immediately and fills the other
	// lane with the four small jobs (makespan 10).
	costs := []float64{1, 1, 1, 1, 10}
	if got := simulateMakespan(costs, 2); got != 12 {
		t.Fatalf("FIFO makespan = %v, want 12", got)
	}
	if got := simulateMakespan(sortedDescending(costs), 2); got != 10 {
		t.Fatalf("LPT makespan = %v, want 10", got)
	}
}

func TestSimulateMakespanLPTWorstCase(t *testing.T) {
	// Four equal jobs on two lanes must split evenly; the longest-first order
	// is what a work-conserving pool follows after sorting.
	costs := []float64{25, 25, 25, 25}
	if got := simulateMakespan(sortedDescending(costs), 2); got != 50 {
		t.Fatalf("LPT makespan = %v, want 50", got)
	}
}

func TestAnalyzeSummaryProjectsLaneArithmetic(t *testing.T) {
	summary := summaryFile{
		Schema:               "pipelinegen.golden-overlay-summary.v1",
		JobID:                "job_test",
		RunID:                "run_test",
		Status:               "SUCCEEDED",
		OverlayPlanItems:     6,
		OverlayRenderBatches: 3,
		OverlayWindow:        &windowBlock{Seconds: 20, ItemsPerMin: 18},
		Batches: []batchBlock{
			{BatchID: "a", SceneID: "s1", ItemIDs: []string{"1", "2", "3"}, RenderMS: 10000, QueueWaitMS: 12000,
				Attribution: &batchAttribution{WorkerMetricsMS: map[string]float64{"total_ms": 12000}}, CacheOutcome: "miss_stored"},
			{BatchID: "b", SceneID: "s2", ItemIDs: []string{"4"}, RenderMS: 5000, QueueWaitMS: 7000,
				Attribution: &batchAttribution{WorkerMetricsMS: map[string]float64{"total_ms": 6000}}, CacheOutcome: "not_applicable"},
			{BatchID: "c", SceneID: "s3", ItemIDs: []string{"5", "6"}, RenderMS: 3000, QueueWaitMS: 5000,
				Attribution: &batchAttribution{WorkerMetricsMS: map[string]float64{"total_ms": 4000}}, CacheOutcome: "not_applicable"},
		},
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "summary.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	run, err := analyzeFile(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	if run.Items != 6 || run.Batches != 3 {
		t.Fatalf("items/batches = %d/%d, want 6/3", run.Items, run.Batches)
	}
	if run.RenderSumMS != 18000 {
		t.Fatalf("render sum = %v, want 18000", run.RenderSumMS)
	}
	if run.RenderFloorMS != 6000 {
		t.Fatalf("render floor = %v, want 6000", run.RenderFloorMS)
	}
	if run.Utilization != 30 {
		t.Fatalf("utilization = %v, want 30", run.Utilization)
	}
	if run.FIFOMakespanMS != 12000 {
		t.Fatalf("FIFO makespan = %v, want 12000", run.FIFOMakespanMS)
	}
	if run.LPTMakespanMS != 12000 {
		t.Fatalf("LPT makespan = %v, want 12000", run.LPTMakespanMS)
	}
	if run.NonRenderMS != 4000 {
		t.Fatalf("non-render sum = %v, want 4000", run.NonRenderMS)
	}
}

func TestBuildReportMedian(t *testing.T) {
	makeSummary := func(job string, rate float64) []byte {
		raw, err := json.Marshal(summaryFile{
			Schema:           "pipelinegen.golden-overlay-summary.v1",
			JobID:            job,
			OverlayPlanItems: 10,
			OverlayWindow:    &windowBlock{Seconds: 20, ItemsPerMin: rate},
			Batches:          []batchBlock{{RenderMS: 10000, ItemIDs: []string{"1"}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	dir := t.TempDir()
	var paths []string
	for i, rate := range []float64{30, 40, 50} {
		path := filepath.Join(dir, string(rune('a'+i))+".json")
		if err := os.WriteFile(path, makeSummary(string(rune('a'+i)), rate), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	report, err := buildReport(paths, 3)
	if err != nil {
		t.Fatal(err)
	}
	if report.Lanes != 3 || len(report.Runs) != 3 {
		t.Fatalf("lanes/runs = %d/%d, want 3/3", report.Lanes, len(report.Runs))
	}
	if report.Median.Rate != 40 {
		t.Fatalf("median rate = %v, want 40", report.Median.Rate)
	}
}
