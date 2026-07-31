package stockpipeline

import (
	"context"
	"testing"

	appmetrics "github.com/Marcuss-ops/PipelineGen/internal/application/processmetrics"
)

type phaseMetricRepository struct {
	metrics []*appmetrics.Metric
}

func (r *phaseMetricRepository) Insert(_ context.Context, metric *appmetrics.Metric) error {
	copyMetric := *metric
	r.metrics = append(r.metrics, &copyMetric)
	return nil
}

func TestStartStockPhaseWithRecorderUsesRunIdentifiers(t *testing.T) {
	repo := &phaseMetricRepository{}
	recorder := appmetrics.NewRecorder(repo)
	ctx := appmetrics.WithRun(context.Background(), "job-stock-1", "parent-stock-1")

	handle := startStockPhaseWithRecorder(ctx, recorder, "stock.search", "fallback-job")
	if handle == nil {
		t.Fatal("startStockPhaseWithRecorder returned nil")
	}
	if err := handle.End(nil); err != nil {
		t.Fatalf("End: %v", err)
	}
	if len(repo.metrics) != 1 {
		t.Fatalf("persisted metrics = %d, want 1", len(repo.metrics))
	}
	got := repo.metrics[0]
	if got.ProcessType != "stock" || got.Provider != "stock" || got.Phase != "stock.search" {
		t.Fatalf("metric identity = process=%q provider=%q phase=%q", got.ProcessType, got.Provider, got.Phase)
	}
	if got.JobID != "job-stock-1" || got.ParentJobID != "parent-stock-1" {
		t.Fatalf("run identifiers = job=%q parent=%q", got.JobID, got.ParentJobID)
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

func TestStartStockPhaseWithRecorderFallsBackToRunnerJobID(t *testing.T) {
	repo := &phaseMetricRepository{}
	recorder := appmetrics.NewRecorder(repo)

	handle := startStockPhaseWithRecorder(context.Background(), recorder, "stock.compose", "runner-job")
	if err := handle.End(nil); err != nil {
		t.Fatalf("End: %v", err)
	}
	if got := repo.metrics[0].JobID; got != "runner-job" {
		t.Fatalf("fallback job id = %q, want runner-job", got)
	}
}
