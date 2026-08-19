package performance

import (
	"context"
	"sync"
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
		if w.Version != WorkloadVersion || w.ParametersJSON == "" || w.Operation == "" {
			t.Fatalf("workload %q missing version, parameters or operation: %+v", w.WorkloadID, w)
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

// fakeSampleSource is a coordinated canonical sample log: the executor
// appends the canonical elapsed_ms it "measured", and the suite reads them
// back through OperationSamples — so the suite never times the execution.
type fakeSampleSource struct {
	mu      sync.Mutex
	samples map[string][]int64
}

func newFakeSampleSource() *fakeSampleSource {
	return &fakeSampleSource{samples: map[string][]int64{}}
}

func (f *fakeSampleSource) OperationSamples(_ context.Context, operation, _ string) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.samples[operation]...), nil
}

func (f *fakeSampleSource) append(operation string, elapsed int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.samples[operation] = append(f.samples[operation], elapsed)
}

var _ OperationSampleSource = (*fakeSampleSource)(nil)

type fakeWorkloadExecutor struct {
	log  *fakeSampleSource
	errs map[string]error
}

func (f fakeWorkloadExecutor) RunWorkload(_ context.Context, w Workload) error {
	if f.errs != nil {
		return f.errs[w.WorkloadID]
	}
	// The executor runs the workload; the canonical duration is written by
	// the ObservedExecutor. Here we record a fixed canonical sample directly.
	f.log.append(w.Operation, 5000)
	return nil
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
		{WorkloadID: Workload1080p10s, Version: WorkloadVersion, Operation: "normalize"},
	}
	source := newFakeSampleSource()
	source.append("normalize", 20000)
	source.append("normalize", 21000)
	source.append("normalize", 19000)
	registry := &recordingRegistry{}
	suite := NewBenchmarkSuite(fakeWorkloadExecutor{log: source}, source, registry, 3)
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
	// Current canonical samples are [5000,5000,5000] (median 5s) — a large
	// improvement over the 20s baseline.
	if c.Verdict != VerdictImproved {
		t.Fatalf("verdict = %s, want IMPROVED (5s vs 20s baseline)", c.Verdict)
	}
	if len(registry.runs) != 1 {
		t.Fatalf("recorded runs = %d, want 1", len(registry.runs))
	}
	run := registry.runs[0]
	if run.WorkloadID != Workload1080p10s || run.WorkloadVersion != WorkloadVersion || run.Status != "SUCCEEDED" || run.WallMS != 5000 {
		t.Fatalf("recorded run = %+v", run)
	}
}

func TestBenchmarkSuiteWithoutBaselineReportsNoBaseline(t *testing.T) {
	source := newFakeSampleSource()
	suite := NewBenchmarkSuite(fakeWorkloadExecutor{log: source}, source, &recordingRegistry{}, 2)
	comparisons, err := suite.Run(context.Background(), []Workload{{WorkloadID: WorkloadWatermark, Operation: "watermark"}})
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
	source := newFakeSampleSource()
	suite := NewBenchmarkSuite(fakeWorkloadExecutor{log: source, errs: map[string]error{Workload1080p10s: context.DeadlineExceeded}}, source, nil, 2)
	if _, err := suite.Run(context.Background(), []Workload{{WorkloadID: Workload1080p10s, Operation: "normalize"}}); err == nil {
		t.Fatal("executor failure must surface")
	}
}
