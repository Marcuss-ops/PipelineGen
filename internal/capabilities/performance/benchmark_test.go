package performance

import (
	"context"
	"testing"
	"time"
)

func TestCanonicalWorkloadsDefinesTheFixedSet(t *testing.T) {
	workloads := CanonicalWorkloads()
	if len(workloads) != 6 {
		t.Fatalf("workloads = %d, want 6", len(workloads))
	}
	seen := map[string]bool{}
	wantIDs := []string{Workload1080p10s, Workload1080p60s, WorkloadWatermark, WorkloadAudioMix, Workload10SceneRender, WorkloadStreamCopy}
	for _, w := range workloads {
		if seen[w.WorkloadID] {
			t.Fatalf("duplicate workload id %q", w.WorkloadID)
		}
		seen[w.WorkloadID] = true
		if w.Version != WorkloadVersion || w.ParametersJSON == "" {
			t.Fatalf("workload %q missing version or parameters: %+v", w.WorkloadID, w)
		}
	}
	for _, id := range wantIDs {
		if !seen[id] {
			t.Fatalf("workload %q missing from the canonical set", id)
		}
	}
}

func TestMedianInt64(t *testing.T) {
	cases := []struct {
		name   string
		values []int64
		want   float64
	}{
		{"empty", nil, 0},
		{"single", []int64{42}, 42},
		{"odd", []int64{5, 1, 3}, 3},
		{"even", []int64{5, 1, 3, 9}, 4},
		{"unsorted even", []int64{100, 300, 200, 400}, 250},
	}
	for _, tc := range cases {
		if got := MedianInt64(tc.values); got != tc.want {
			t.Errorf("%s: median = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCompareBaseline(t *testing.T) {
	cases := []struct {
		name     string
		previous []int64
		current  []int64
		verdict  string
	}{
		{"no baseline", nil, []int64{100}, VerdictNoBaseline},
		{"improved", []int64{12100, 12000, 12200}, []int64{8400, 8300, 8500}, VerdictImproved},
		{"regressed", []int64{6200, 6100, 6300}, []int64{6800, 6700, 6900}, VerdictRegressed},
		{"unchanged", []int64{10000}, []int64{10050}, VerdictUnchanged},
	}
	for _, tc := range cases {
		got := CompareBaseline(tc.previous, tc.current)
		if got.Verdict != tc.verdict {
			t.Errorf("%s: verdict = %s, want %s (delta %.2f%%)", tc.name, got.Verdict, tc.verdict, got.DeltaPercent)
		}
	}
	// Improvement example from the spec: 12.1s → 8.4s ≈ -30.58% faster.
	improved := CompareBaseline([]int64{12100}, []int64{8400})
	if improved.Verdict != VerdictImproved {
		t.Fatalf("improvement verdict = %s", improved.Verdict)
	}
	if improved.DeltaPercent > -30.4 || improved.DeltaPercent < -30.7 {
		t.Fatalf("improvement delta = %.2f%%, want ≈ -30.58%%", improved.DeltaPercent)
	}
}

// ── Suite ────────────────────────────────────────────────────────────

type fakeWorkloadExecutor struct {
	errs map[string]error
}

func (f fakeWorkloadExecutor) RunWorkload(_ context.Context, w Workload) error {
	if f.errs != nil {
		return f.errs[w.WorkloadID]
	}
	// Non-zero wall time so samples are real (not a degenerate 0ms median).
	time.Sleep(5 * time.Millisecond)
	return nil
}

type fakeBaselineSource struct{ samples map[string][]int64 }

func (f fakeBaselineSource) WorkloadSamples(_ context.Context, workloadID string) ([]int64, error) {
	return f.samples[workloadID], nil
}

type recordingRegistry struct{ runs []Run }

func (r *recordingRegistry) RecordRun(_ context.Context, run Run) error {
	r.runs = append(r.runs, run)
	return nil
}
func (r *recordingRegistry) RecordStep(context.Context, Step) error { return nil }
func (r *recordingRegistry) RecordArtifact(context.Context, Artifact) error {
	return nil
}
func (r *recordingRegistry) RegisterWorkload(context.Context, Workload) error { return nil }

var _ Registry = (*recordingRegistry)(nil)

func TestBenchmarkSuiteRunsAndCompares(t *testing.T) {
	workloads := []Workload{
		{WorkloadID: Workload1080p10s, Version: WorkloadVersion, ParametersJSON: `{"operation":"normalize"}`},
	}
	baselines := fakeBaselineSource{samples: map[string][]int64{Workload1080p10s: {20000, 21000, 19000}}}
	registry := &recordingRegistry{}
	suite := NewBenchmarkSuite(fakeWorkloadExecutor{}, baselines, registry, 3)
	suite.SetClock(func() time.Time { return time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC) })

	comparisons, err := suite.Run(context.Background(), workloads)
	if err != nil {
		t.Fatal(err)
	}
	if len(comparisons) != 1 {
		t.Fatalf("comparisons = %d", len(comparisons))
	}
	c := comparisons[0]
	if c.WorkloadID != Workload1080p10s {
		t.Fatalf("workload id = %q", c.WorkloadID)
	}
	if c.PreviousMedianMS != 20000 {
		t.Fatalf("previous median = %v, want 20000", c.PreviousMedianMS)
	}
	// The fake executor returns instantly, so the current median is ~0ms:
	// a large improvement over the 20s baseline.
	if c.Verdict != VerdictImproved {
		t.Fatalf("verdict = %s, want IMPROVED (instant vs 20s baseline)", c.Verdict)
	}
	if len(registry.runs) != 1 {
		t.Fatalf("recorded runs = %d, want 1", len(registry.runs))
	}
	run := registry.runs[0]
	if run.WorkloadID != Workload1080p10s || run.WorkloadVersion != WorkloadVersion || run.Status != "SUCCEEDED" || run.WallMS < 0 {
		t.Fatalf("recorded run = %+v", run)
	}
}

func TestBenchmarkSuiteWithoutBaselineReportsNoBaseline(t *testing.T) {
	suite := NewBenchmarkSuite(fakeWorkloadExecutor{}, fakeBaselineSource{}, &recordingRegistry{}, 2)
	comparisons, err := suite.Run(context.Background(), []Workload{{WorkloadID: WorkloadWatermark}})
	if err != nil {
		t.Fatal(err)
	}
	if len(comparisons) != 1 || comparisons[0].Verdict != VerdictNoBaseline {
		t.Fatalf("comparisons = %+v, want NO_BASELINE", comparisons)
	}
}

func TestBenchmarkSuiteRequiresExecutor(t *testing.T) {
	suite := NewBenchmarkSuite(nil, nil, nil, 1)
	if _, err := suite.Run(context.Background(), CanonicalWorkloads()); err == nil {
		t.Fatal("a nil executor must fail closed")
	}
}

func TestBenchmarkSuitePropagatesExecutorFailure(t *testing.T) {
	suite := NewBenchmarkSuite(fakeWorkloadExecutor{errs: map[string]error{Workload1080p10s: context.DeadlineExceeded}}, fakeBaselineSource{}, nil, 2)
	if _, err := suite.Run(context.Background(), []Workload{{WorkloadID: Workload1080p10s}}); err == nil {
		t.Fatal("executor failure must surface")
	}
}
