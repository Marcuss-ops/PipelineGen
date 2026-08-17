package adapters

import (
	"context"
	"testing"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

type parityProbe struct{ observations []TimingParity }

func (p *parityProbe) ObserveTimingParity(v TimingParity) { p.observations = append(p.observations, v) }
func TestCanonicalTimingAdapterProjectsOneAuthoritativeDuration(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "j", AttemptID: "a"})
	ctx := kernobs.WithRun(context.Background(), run)
	p := &parityProbe{}
	r := &PipelineResult{}
	a := &CanonicalTimingAdapter{Parity: p}
	st, err := a.MeasureCanonical(ctx, "metadata", func(context.Context) error { time.Sleep(time.Millisecond); return nil })
	if err != nil {
		t.Fatal(err)
	}
	_ = a.ProjectStageWithLegacy(ctx, r, "metadata", st, st.DurationMs)
	if r.StageDurations["metadata"] != st.DurationMs || len(p.observations) != 1 || !p.observations[0].Match || len(run.Report().Stages) != 1 {
		t.Fatalf("result=%#v parity=%#v stages=%d", r, p.observations, len(run.Report().Stages))
	}
}
func TestCanonicalTimingAdapterProjectsGenerationTimings(t *testing.T) {
	a := &CanonicalTimingAdapter{}
	var timings scriptpkg.GenerationTimings
	a.ProjectGenerationTimings(&timings, "source.resolve", kernobs.StageReport{DurationMs: 1840})
	a.ProjectGenerationTimings(&timings, "script.plan", kernobs.StageReport{DurationMs: 365})
	a.ProjectGenerationTimings(&timings, "script.engine", kernobs.StageReport{DurationMs: 79320})
	a.ProjectGenerationTimings(&timings, "unknown.stage", kernobs.StageReport{DurationMs: 999})
	if timings.SourceResolveMs != 1840 || timings.PlanBuildMs != 365 || timings.EngineMs != 79320 {
		t.Fatalf("timings = %+v, want SourceResolveMs=1840 PlanBuildMs=365 EngineMs=79320", timings)
	}
	if timings.TotalMs != 0 {
		t.Fatalf("TotalMs = %d, want 0 (TotalMs is the canonical run wall, not a stage)", timings.TotalMs)
	}

	var nilTimings *scriptpkg.GenerationTimings
	a.ProjectGenerationTimings(nilTimings, "source.resolve", kernobs.StageReport{DurationMs: 1})
	if nilTimings != nil {
		t.Fatal("nil timings must remain nil (no-op projection)")
	}
}

func TestCanonicalTimingAdapterParityMismatchIsDiagnosticOnly(t *testing.T) {
	p := &parityProbe{}
	r := &PipelineResult{}
	a := &CanonicalTimingAdapter{Parity: p}
	st := kernobs.StageReport{Name: "entities", Status: kernobs.StageStatusCompleted, DurationMs: 42}
	_ = a.ProjectStageWithLegacy(context.Background(), r, "entities", st, 99)
	if r.StageDurations["entities"] != 42 || len(p.observations) != 1 || p.observations[0].Match {
		t.Fatalf("result=%#v parity=%#v", r, p.observations)
	}
}
