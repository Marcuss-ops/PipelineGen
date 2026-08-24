package adapters

import (
	"context"
	"testing"
	"time"

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
	if len(p.observations) != 1 || !p.observations[0].Match || len(run.Report().Stages) != 1 {
		t.Fatalf("result=%#v parity=%#v stages=%d", r, p.observations, len(run.Report().Stages))
	}
}
func TestCanonicalTimingAdapterParityMismatchIsDiagnosticOnly(t *testing.T) {
	p := &parityProbe{}
	r := &PipelineResult{}
	a := &CanonicalTimingAdapter{Parity: p}
	st := kernobs.StageReport{Name: "entities", Status: kernobs.StageStatusCompleted, DurationMs: 42}
	_ = a.ProjectStageWithLegacy(context.Background(), r, "entities", st, 99)
	if len(p.observations) != 1 || p.observations[0].Match {
		t.Fatalf("result=%#v parity=%#v", r, p.observations)
	}
}
