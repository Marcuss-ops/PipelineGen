package performance

import (
	"context"
	"errors"
	"testing"

	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

func reportWithWall(jobID string, wallMS int64, mixMS int64) PerformanceReport {
	run := kernobs.RunReport{JobID: jobID, WallTimeMs: wallMS}
	audio := scriptgeneration.AudioPipelineMetrics{MixMS: mixMS}
	return Build(run, audio, nil, DefaultPhaseResolver{})
}

func TestBuildProjectsEnvelope(t *testing.T) {
	run := kernobs.RunReport{JobID: "job-1", WallTimeMs: 87431, QueueWaitMs: 1200}
	audio := fullAudio()
	steps := []scriptgeneration.ExecutionStep{{Name: "DOCUMENT", Status: "COMPLETED", DurationMS: 1241}}

	r := Build(run, audio, steps, DefaultPhaseResolver{})
	if r.JobID != "job-1" || r.WallTimeMS != 87431 {
		t.Errorf("envelope = job %s wall %d", r.JobID, r.WallTimeMS)
	}
	if r.Waits.QueueMS != 1200 {
		t.Errorf("queue wait = %d, want 1200", r.Waits.QueueMS)
	}
	if len(r.Phases) != len(Phases()) {
		t.Errorf("phases = %d, want %d", len(r.Phases), len(Phases()))
	}
	if r.Audio.RTF != 0.2 {
		t.Errorf("audio RTF = %v, want 0.2", r.Audio.RTF)
	}
}

func TestAggregateComputesStatsAndPctWall(t *testing.T) {
	reports := []PerformanceReport{
		reportWithWall("a", 1000, 100),
		reportWithWall("b", 2000, 300),
		reportWithWall("c", 3000, 500),
	}

	agg := Aggregate(reports)

	if len(agg.JobIDs) != 3 {
		t.Fatalf("job ids = %v", agg.JobIDs)
	}

	// Wall: 1000, 2000, 3000 → min 1000, median 2000, avg 2000, max 3000.
	if agg.Wall.MinMS != 1000 || agg.Wall.MedianMS != 2000 || agg.Wall.AvgMS != 2000 || agg.Wall.MaxMS != 3000 {
		t.Errorf("wall stats = %+v", agg.Wall)
	}

	var mix PhaseStats
	for _, p := range agg.Phases {
		if p.Phase == PhaseRustMix {
			mix = p
		}
	}
	if mix.MeasuredJobs != 3 {
		t.Errorf("mix measured jobs = %d, want 3", mix.MeasuredJobs)
	}
	if mix.MinMS != 100 || mix.MedianMS != 300 || mix.MaxMS != 500 {
		t.Errorf("mix stats = %+v", mix)
	}
	// avg = 300, wall avg = 2000 → pct_wall = 15%.
	if mix.PctWall != 15 {
		t.Errorf("mix pct_wall = %v, want 15", mix.PctWall)
	}
	if mix.AvgMS != 300 {
		t.Errorf("mix avg = %v, want 300", mix.AvgMS)
	}
	if mix.P95MS != 500 { // nearest-rank p95 of 3 values → max
		t.Errorf("mix p95 = %d, want 500", mix.P95MS)
	}
}

func TestPhaseStatsComputesAllAggregates(t *testing.T) {
	values := []int64{100, 200, 300, 400, 500}
	st := phaseStats(PhaseRustMix, values, 2000)

	if st.MeasuredJobs != 5 {
		t.Errorf("measured jobs = %d, want 5", st.MeasuredJobs)
	}
	if st.MinMS != 100 || st.MaxMS != 500 {
		t.Errorf("min/max = %d/%d, want 100/500", st.MinMS, st.MaxMS)
	}
	if st.MedianMS != 300 {
		t.Errorf("median = %d, want 300", st.MedianMS)
	}
	if st.AvgMS != 300 {
		t.Errorf("avg = %v, want 300", st.AvgMS)
	}
	if st.P95MS != 500 { // nearest-rank p95 of 5 values → max
		t.Errorf("p95 = %d, want 500", st.P95MS)
	}
	if st.PctWall != 15 {
		t.Errorf("pct_wall = %v, want 15", st.PctWall)
	}
}

func TestPercentileNearestRank(t *testing.T) {
	sorted := []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	cases := []struct {
		p    float64
		want int64
	}{
		{0, 10}, {50, 50}, {95, 100}, {100, 100},
	}
	for _, c := range cases {
		if got := percentile(sorted, c.p); got != c.want {
			t.Errorf("percentile(%v) = %d, want %d", c.p, got, c.want)
		}
	}
}

func TestAggregateSkipsUnmeasuredPhases(t *testing.T) {
	// Only job "a" measures mix; job "b" measures nothing.
	reports := []PerformanceReport{
		reportWithWall("a", 1000, 100),
		reportWithWall("b", 1000, 0),
	}

	agg := Aggregate(reports)
	var mix PhaseStats
	for _, p := range agg.Phases {
		if p.Phase == PhaseRustMix {
			mix = p
		}
	}
	if mix.MeasuredJobs != 1 {
		t.Errorf("mix measured jobs = %d, want 1", mix.MeasuredJobs)
	}
	if mix.MinMS != 100 || mix.MedianMS != 100 || mix.MaxMS != 100 {
		t.Errorf("mix stats = %+v, want single-value 100", mix)
	}
}

func TestBuildSurfacesUnmeasuredPhasesExplicitly(t *testing.T) {
	// No canonical source populated: every phase must be named in the
	// unmeasured list with its source, not silently dropped or estimated.
	r := Build(kernobs.RunReport{JobID: "job-empty"}, scriptgeneration.AudioPipelineMetrics{}, nil, DefaultPhaseResolver{})

	if len(r.Unmeasured) != len(Phases()) {
		t.Fatalf("unmeasured = %d phases, want %d", len(r.Unmeasured), len(Phases()))
	}
	for i, u := range r.Unmeasured {
		if u.Phase != Phases()[i] {
			t.Errorf("unmeasured[%d] = %q, want %q in canonical order", i, u.Phase, Phases()[i])
		}
		if u.Source == "" {
			t.Errorf("unmeasured phase %q has empty source", u.Phase)
		}
	}
}

func TestBuildPopulatesScriptSummary(t *testing.T) {
	run := kernobs.RunReport{
		JobID: "job-script",
		Operations: []kernobs.OperationReport{
			{Component: string(kernobs.ComponentOllama), Operation: string(kernobs.OperationGenerate), DurationMs: 10000},
		},
	}
	steps := []scriptgeneration.ExecutionStep{{Name: "SCRIPT", Status: "COMPLETED", DurationMS: 12000}}

	r := Build(run, scriptgeneration.AudioPipelineMetrics{}, steps, DefaultPhaseResolver{})
	if r.Script.TotalMS != 12000 || r.Script.InferenceMS != 10000 || r.Script.OverheadMS != 2000 {
		t.Errorf("script = %+v, want total=12000 inference=10000 overhead=2000", r.Script)
	}
}

func TestBuildOmitsMeasuredPhasesFromUnmeasured(t *testing.T) {
	audio := scriptgeneration.AudioPipelineMetrics{MixMS: 500}
	r := Build(kernobs.RunReport{JobID: "job-mix"}, audio, nil, DefaultPhaseResolver{})

	for _, u := range r.Unmeasured {
		if u.Phase == PhaseRustMix {
			t.Errorf("measured phase %q must not appear in unmeasured list", u.Phase)
		}
	}
}

func TestAggregateSurfacesNeverMeasuredPhases(t *testing.T) {
	// One job, no measurements: the aggregate must name every phase as
	// unmeasured rather than emitting all-zero stats as if it were data.
	agg := Aggregate([]PerformanceReport{reportWithWall("a", 1000, 0)})

	if len(agg.Unmeasured) != len(Phases()) {
		t.Fatalf("aggregate unmeasured = %d, want %d", len(agg.Unmeasured), len(Phases()))
	}
	for i, p := range agg.Unmeasured {
		if p != Phases()[i] {
			t.Errorf("aggregate unmeasured[%d] = %q, want %q", i, p, Phases()[i])
		}
	}
}

type fakeSource struct {
	load func(ctx context.Context, jobID string) (kernobs.RunReport, scriptgeneration.AudioPipelineMetrics, []scriptgeneration.ExecutionStep, error)
}

func (f fakeSource) Load(ctx context.Context, jobID string) (kernobs.RunReport, scriptgeneration.AudioPipelineMetrics, []scriptgeneration.ExecutionStep, error) {
	return f.load(ctx, jobID)
}

func TestAggregatorCompare(t *testing.T) {
	src := fakeSource{load: func(_ context.Context, jobID string) (kernobs.RunReport, scriptgeneration.AudioPipelineMetrics, []scriptgeneration.ExecutionStep, error) {
		return kernobs.RunReport{JobID: jobID, WallTimeMs: 1000}, scriptgeneration.AudioPipelineMetrics{MixMS: 200}, nil, nil
	}}
	agg := NewAggregator(src, nil)

	got, err := agg.Compare(context.Background(), []string{"j1", "j2"})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if len(got.JobIDs) != 2 || got.JobIDs[0] != "j1" || got.JobIDs[1] != "j2" {
		t.Errorf("job ids = %v", got.JobIDs)
	}
}

func TestAggregatorSurfacesLoadError(t *testing.T) {
	src := fakeSource{load: func(context.Context, string) (kernobs.RunReport, scriptgeneration.AudioPipelineMetrics, []scriptgeneration.ExecutionStep, error) {
		return kernobs.RunReport{}, scriptgeneration.AudioPipelineMetrics{}, nil, errors.New("boom")
	}}
	agg := NewAggregator(src, nil)

	if _, err := agg.BuildJobReport(context.Background(), "j1"); err == nil {
		t.Error("expected load error to propagate")
	}
}

func TestAggregatorWithoutSourceFailsClosed(t *testing.T) {
	agg := NewAggregator(nil, nil)
	if _, err := agg.BuildJobReport(context.Background(), "j1"); err == nil {
		t.Error("expected missing-source error")
	}
}
