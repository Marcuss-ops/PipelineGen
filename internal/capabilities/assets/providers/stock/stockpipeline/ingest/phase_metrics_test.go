package ingest

import (
	"context"
	"testing"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

func TestStartStockPhaseRecordsCanonicalStage(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{
		JobID: "job-stock-1", AttemptID: "attempt-stock-1",
	})
	ctx := kernobs.WithRun(context.Background(), run)

	handle := startStockPhase(ctx, nil, "stock.search")
	if handle == nil {
		t.Fatal("startStockPhase returned nil")
	}
	handle.SetItems(3, 3)
	handle.SetItemsFailed(0)
	got := handle.End(nil)
	if got.Name != "stock.search" || got.Status != kernobs.StageStatusCompleted {
		t.Fatalf("stage = %#v, want completed stock.search", got)
	}
	if got.ItemsInput != 3 || got.ItemsCompleted != 3 {
		t.Fatalf("stage counters = %d/%d, want 3/3", got.ItemsInput, got.ItemsCompleted)
	}
	if len(run.Report().Stages) != 1 {
		t.Fatalf("canonical stage count = %d, want 1", len(run.Report().Stages))
	}
}

func TestCountUniquePlanSources(t *testing.T) {
	plans := []ClipPlan{
		{SourceID: "video-a", StartSec: 0, EndSec: 5},
		{SourceID: "video-a", StartSec: 5, EndSec: 10},
		{SourceID: "video-b", StartSec: 0, EndSec: 5},
	}
	if got := countUniquePlanSources(plans); got != 2 {
		t.Fatalf("countUniquePlanSources = %d, want 2", got)
	}
}

func TestStartStockPhaseFailureUsesCanonicalError(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{
		JobID: "job-stock-2", AttemptID: "attempt-stock-2",
	})
	ctx := kernobs.WithRun(context.Background(), run)

	handle := startStockPhase(ctx, nil, "stock.compose")
	if handle == nil {
		t.Fatal("startStockPhase returned nil")
	}
	got := handle.End(context.Canceled)
	if got.Status != kernobs.StageStatusFailed {
		t.Fatalf("stage status = %q, want failed", got.Status)
	}
	if got.ErrorCode == "" {
		t.Fatal("failed canonical stage must include an error code")
	}
}
