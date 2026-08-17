package scriptgeneration

import (
	"context"
	"testing"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

func TestMeasurePhaseRecordsStageWallTime(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-1", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)
	r := &Runner{}

	ok := r.measurePhase(ctx, kernobs.StageGenerate, func(c context.Context) bool { return true })
	if !ok {
		t.Fatal("measurePhase returned false for a successful phase")
	}
	run.Finish()

	stages := run.Report().Stages
	if len(stages) != 1 {
		t.Fatalf("stages = %d, want 1", len(stages))
	}
	st := stages[0]
	if st.Name != string(kernobs.StageGenerate) {
		t.Fatalf("stage name = %q, want %q", st.Name, kernobs.StageGenerate)
	}
	if st.Status != kernobs.StageStatusCompleted {
		t.Fatalf("stage status = %q, want completed", st.Status)
	}
	if st.StartedAt.IsZero() || st.FinishedAt.IsZero() {
		t.Fatal("stage must carry a StartedAt/FinishedAt wall interval")
	}
	if st.FinishedAt.Before(st.StartedAt) {
		t.Fatal("stage FinishedAt before StartedAt")
	}
	if st.DurationMs < 0 {
		t.Fatalf("stage duration = %d, want >= 0", st.DurationMs)
	}
}

func TestMeasurePhaseFailurePropagatesAndMarksFailed(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-1", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)
	r := &Runner{}

	ok := r.measurePhase(ctx, kernobs.StageGenerate, func(c context.Context) bool { return false })
	if ok {
		t.Fatal("measurePhase returned true for a failed phase")
	}
	run.Finish()

	stages := run.Report().Stages
	if len(stages) != 1 {
		t.Fatalf("stages = %d, want 1", len(stages))
	}
	if stages[0].Status != kernobs.StageStatusFailed {
		t.Fatalf("stage status = %q, want failed", stages[0].Status)
	}
}

func TestMeasurePhaseNoRunIsNoop(t *testing.T) {
	r := &Runner{}
	called := false
	ok := r.measurePhase(context.Background(), kernobs.StageGenerate, func(c context.Context) bool {
		called = true
		return true
	})
	if !ok || !called {
		t.Fatalf("no-run measurePhase must pass through unchanged: ok=%t called=%t", ok, called)
	}
}
