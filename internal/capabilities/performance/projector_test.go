package performance

import (
	"context"
	"testing"
	"time"

	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	perf "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/performance"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

func TestProjectMapsMeasuredPhasesToSteps(t *testing.T) {
	started := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	finished := started.Add(90 * time.Second)
	run := kernobs.RunReport{
		RunID:      "run-1",
		JobID:      "job-1",
		JobType:    "script.generate",
		Status:     kernobs.StatusSucceeded,
		WorkerID:   "worker-77",
		StartedAt:  started,
		FinishedAt: finished,
		WallTimeMs: 90000,
	}
	report := perf.PerformanceReport{
		JobID:      "job-1",
		WallTimeMS: 90000,
		Phases: []perf.PhaseMeasurement{
			{Phase: perf.PhaseEdgeTTS, DurationMS: 44000, Measured: true, Counters: map[string]float64{"tts_calls": 14}},
			{Phase: perf.PhaseRustMix, DurationMS: 1100, Measured: true},
			{Phase: perf.PhaseProbe, DurationMS: 0, Measured: false},
		},
	}

	runRow, steps := Project(run, report, JobMeta{RootJobID: "root-1", VideoID: "v-1", GitSHA: "abc", HostID: "host-1"})

	if runRow.RunID != "run-1" || runRow.JobID != "job-1" || runRow.Status != "SUCCEEDED" || runRow.WallMS != 90000 {
		t.Fatalf("run = %+v", runRow)
	}
	if runRow.RootJobID != "root-1" || runRow.VideoID != "v-1" || runRow.GitSHA != "abc" || runRow.HostID != "host-1" || runRow.WorkerID != "worker-77" {
		t.Fatalf("correlation fields = %+v", runRow)
	}
	if runRow.StartedAt == "" || runRow.CompletedAt == "" {
		t.Fatalf("timestamps must be populated: %+v", runRow)
	}
	if len(steps) != 2 {
		t.Fatalf("steps = %d, want 2 (only measured phases become steps)", len(steps))
	}
	if steps[0].Name != string(perf.PhaseEdgeTTS) || steps[0].DurationMS != 44000 || steps[0].Status != "SUCCEEDED" {
		t.Fatalf("steps[0] = %+v", steps[0])
	}
	if steps[0].StepID != "run-1:edge_tts" || steps[0].RunID != "run-1" || steps[0].JobID != "job-1" {
		t.Fatalf("steps[0] identity = %+v", steps[0])
	}
	if steps[1].Name != string(perf.PhaseRustMix) || steps[1].DurationMS != 1100 {
		t.Fatalf("steps[1] = %+v", steps[1])
	}
}

func TestProjectMapsCancelledToFailed(t *testing.T) {
	now := time.Now().UTC()
	run := kernobs.RunReport{RunID: "run-c", JobID: "job-c", Status: kernobs.StatusCancelled, StartedAt: now, FinishedAt: now.Add(time.Second)}
	runRow, _ := Project(run, perf.PerformanceReport{}, JobMeta{})
	if runRow.Status != "FAILED" {
		t.Fatalf("cancelled run status = %q, want FAILED", runRow.Status)
	}
}

type fakeReportSource struct {
	run   kernobs.RunReport
	audio scriptgeneration.AudioPipelineMetrics
	steps []scriptgeneration.ExecutionStep
	err   error
}

func (f fakeReportSource) Load(context.Context, string) (kernobs.RunReport, scriptgeneration.AudioPipelineMetrics, []scriptgeneration.ExecutionStep, error) {
	return f.run, f.audio, f.steps, f.err
}

type fakeRegistry struct {
	runs  []Run
	steps []Step
}

func (f *fakeRegistry) RecordRun(_ context.Context, r Run) error {
	f.runs = append(f.runs, r)
	return nil
}
func (f *fakeRegistry) RecordStep(_ context.Context, s Step) error {
	f.steps = append(f.steps, s)
	return nil
}
func (f *fakeRegistry) RecordArtifact(context.Context, Artifact) error   { return nil }
func (f *fakeRegistry) RegisterWorkload(context.Context, Workload) error { return nil }

func TestProjectorPersistsRunAndMeasuredSteps(t *testing.T) {
	now := time.Now().UTC()
	src := fakeReportSource{
		run: kernobs.RunReport{
			RunID:      "run-1",
			JobID:      "job-1",
			Status:     kernobs.StatusSucceeded,
			StartedAt:  now,
			FinishedAt: now.Add(5 * time.Second),
			WallTimeMs: 5000,
		},
		audio: scriptgeneration.AudioPipelineMetrics{MixMS: 1000},
	}
	reg := &fakeRegistry{}
	p := NewProjector(src, reg)

	_, steps, err := p.ProjectJob(context.Background(), "job-1", JobMeta{RootJobID: "root-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.runs) != 1 || reg.runs[0].JobID != "job-1" || reg.runs[0].RootJobID != "root-1" || reg.runs[0].WallMS != 5000 {
		t.Fatalf("runs = %+v", reg.runs)
	}
	if len(reg.steps) != 1 || reg.steps[0].Name != string(perf.PhaseRustMix) || reg.steps[0].DurationMS != 1000 {
		t.Fatalf("steps = %+v", reg.steps)
	}
	if len(steps) != 1 {
		t.Fatalf("returned steps = %d, want 1", len(steps))
	}
}

func TestProjectorSkipsNonFinalizedRun(t *testing.T) {
	src := fakeReportSource{
		run: kernobs.RunReport{RunID: "run-running", JobID: "job-running", Status: kernobs.StatusRunning, StartedAt: time.Now().UTC()},
	}
	reg := &fakeRegistry{}
	p := NewProjector(src, reg)

	if _, _, err := p.ProjectJob(context.Background(), "job-running", JobMeta{}); err == nil {
		t.Fatal("non-finalized run must fail closed, not persist a RUNNING row")
	}
	if len(reg.runs) != 0 {
		t.Fatalf("non-finalized run must not be recorded, got %+v", reg.runs)
	}
}

func TestProjectorFailsClosedWithoutSourceOrRegistry(t *testing.T) {
	if _, _, err := NewProjector(nil, &fakeRegistry{}).ProjectJob(context.Background(), "job-1", JobMeta{}); err == nil {
		t.Fatal("nil report source must fail closed")
	}
	if _, _, err := NewProjector(fakeReportSource{}, nil).ProjectJob(context.Background(), "job-1", JobMeta{}); err == nil {
		t.Fatal("nil registry must fail closed")
	}
}
