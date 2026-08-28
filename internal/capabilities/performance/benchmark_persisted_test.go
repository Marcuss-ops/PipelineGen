package performance

import (
	"context"
	"testing"
)

type persistedReportReader struct {
	reports map[string]PerformanceReport
	calls   int
}

func (r *persistedReportReader) PerformanceReport(_ context.Context, jobID string) (PerformanceReport, error) {
	r.calls++
	return r.reports[jobID], nil
}

type failingExecutor struct{ called bool }

func (e *failingExecutor) RunWorkload(context.Context, Workload) error { e.called = true; return nil }

func TestBenchmarkSuiteReadsPersistedPerformanceReportsOnly(t *testing.T) {
	executor := &failingExecutor{}
	reader := &persistedReportReader{reports: map[string]PerformanceReport{
		Workload1080p10s: {JobID: Workload1080p10s, Job: JobReport{WallTimeMS: 12000}, Derived: DerivedReport{XRT: .75, SpeedFactor: 1.333, CacheRatio: .5, CriticalPathPercent: 20, ParallelismEfficiency: .8, ClipsPerMinute: 5}},
	}}
	suite := NewBenchmarkSuite(executor, nil, nil, 1).WithPerformanceReports(reader)
	comparisons, err := suite.Run(context.Background(), []Workload{{WorkloadID: Workload1080p10s, Operation: "render"}})
	if err != nil {
		t.Fatal(err)
	}
	if executor.called {
		t.Fatal("persisted benchmark mode must not execute the workload")
	}
	if reader.calls != 1 {
		t.Fatalf("report reads=%d, want 1", reader.calls)
	}
	if len(comparisons) != 1 || comparisons[0].WorkloadID != Workload1080p10s {
		t.Fatalf("comparisons=%+v", comparisons)
	}
}
