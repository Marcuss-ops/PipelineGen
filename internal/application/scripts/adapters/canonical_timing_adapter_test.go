package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	appmetrics "github.com/Marcuss-ops/PipelineGen/internal/application/processmetrics"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

type parityProbe struct {
	observations []TimingParity
}

func (p *parityProbe) ObserveTimingParity(observation TimingParity) {
	p.observations = append(p.observations, observation)
}

type canonicalMetricProbe struct {
	samples []appmetrics.CanonicalMetric
}

func (p *canonicalMetricProbe) RecordCanonical(_ context.Context, sample appmetrics.CanonicalMetric) error {
	p.samples = append(p.samples, sample)
	return nil
}

func TestCanonicalTimingAdapterProjectsOneAuthoritativeDuration(t *testing.T) {
	observer := kernobs.NewRunObserver(nil)
	run := observer.StartRun(context.Background(), kernobs.RunInfo{JobID: "job-canonical", AttemptID: "attempt-canonical"})
	ctx := kernobs.WithRun(context.Background(), run)
	result := &PipelineResult{}
	metrics := &canonicalMetricProbe{}
	parity := &parityProbe{}
	adapter := &CanonicalTimingAdapter{ProcessMetrics: metrics, Parity: parity}

	stage, err := adapter.MeasureCanonical(ctx, "metadata", func(context.Context) error {
		time.Sleep(2 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("MeasureCanonical: %v", err)
	}
	adapter.ProjectStageWithLegacy(ctx, result, "metadata", stage, stage.DurationMs)

	if got := result.StageDurations["metadata"]; got != stage.DurationMs {
		t.Fatalf("legacy duration = %d, canonical = %d", got, stage.DurationMs)
	}
	if len(metrics.samples) != 1 || metrics.samples[0].DurationMs != stage.DurationMs {
		t.Fatalf("processmetrics samples = %#v, want one canonical duration", metrics.samples)
	}
	if len(parity.observations) != 1 || !parity.observations[0].Match {
		t.Fatalf("parity observations = %#v, want exact match", parity.observations)
	}
	if len(run.Report().Stages) != 1 {
		t.Fatalf("canonical stage count = %d, want 1", len(run.Report().Stages))
	}
}

func TestCanonicalTimingAdapterParityMismatchIsDiagnosticOnly(t *testing.T) {
	parity := &parityProbe{}
	result := &PipelineResult{}
	adapter := &CanonicalTimingAdapter{Parity: parity}
	stage := kernobs.StageReport{Name: "entities", Status: kernobs.StageStatusCompleted, DurationMs: 42}
	adapter.ProjectStageWithLegacy(context.Background(), result, "entities", stage, 99)

	if result.StageDurations["entities"] != 42 {
		t.Fatalf("canonical report was changed by parity mismatch: %#v", result.StageDurations)
	}
	if len(parity.observations) != 1 || parity.observations[0].Match {
		t.Fatalf("parity mismatch = %#v, want one mismatch", parity.observations)
	}
	if parity.observations[0].CanonicalMs != 42 || parity.observations[0].LegacyMs != 99 {
		t.Fatalf("parity values = %#v", parity.observations[0])
	}
}

func TestCanonicalRecorderUsesSuppliedDuration(t *testing.T) {
	repo := &canonicalMetricRepository{}
	recorder := appmetrics.NewRecorder(repo)
	sample := appmetrics.CanonicalMetric{
		ProcessType: "script",
		JobID:       "job-1",
		Phase:       "entities",
		StartedAt:   time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC),
		DurationMs:  73,
		Status:      "success",
		CreatedAt:   time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC).Add(73 * time.Millisecond),
	}
	if err := recorder.RecordCanonical(context.Background(), sample); err != nil {
		t.Fatalf("RecordCanonical: %v", err)
	}
	if len(repo.metrics) != 1 || repo.metrics[0].DurationMs != 73 {
		t.Fatalf("persisted canonical metrics = %#v", repo.metrics)
	}
}

type canonicalMetricRepository struct {
	metrics []*appmetrics.Metric
}

func (r *canonicalMetricRepository) Insert(_ context.Context, metric *appmetrics.Metric) error {
	if metric == nil {
		return errors.New("nil metric")
	}
	copyMetric := *metric
	r.metrics = append(r.metrics, &copyMetric)
	return nil
}
