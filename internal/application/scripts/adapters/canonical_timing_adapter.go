package adapters

import (
	"context"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

type TimingParity struct {
	Name        string
	CanonicalMs int64
	LegacyMs    int64
	Match       bool
}

type TimingParitySink interface {
	ObserveTimingParity(TimingParity)
}

type CanonicalTimingAdapter struct {
	VidRush VidRushTimingMetrics
	Parity  TimingParitySink
}

func (a *CanonicalTimingAdapter) MeasureCanonical(ctx context.Context, name string, fn func(context.Context) error) (kernobs.StageReport, error) {
	return kernobs.MeasureStageReport(ctx, kernobs.StageName(name), fn)
}

func (a *CanonicalTimingAdapter) ProjectStage(ctx context.Context, result *PipelineResult, name string, stage kernobs.StageReport) error {
	return a.projectStage(ctx, result, name, stage)
}

func (a *CanonicalTimingAdapter) ProjectStageWithLegacy(ctx context.Context, result *PipelineResult, name string, stage kernobs.StageReport, legacyMs int64) error {
	if err := a.projectStage(ctx, result, name, stage); err != nil {
		return err
	}
	if a == nil || a.Parity == nil || legacyMs <= 0 {
		return nil
	}
	a.Parity.ObserveTimingParity(TimingParity{
		Name: name, CanonicalMs: stage.DurationMs, LegacyMs: legacyMs,
		Match: stage.DurationMs == legacyMs,
	})
	return nil
}

func (a *CanonicalTimingAdapter) projectStage(ctx context.Context, result *PipelineResult, name string, stage kernobs.StageReport) error {
	if result != nil {
		if result.StageDurations == nil {
			result.StageDurations = make(map[string]int64)
		}
		result.StageDurations[name] = stage.DurationMs
	}
	if a == nil {
		return nil
	}
	if a.VidRush != nil {
		a.VidRush.ObserveProcessorDuration(name, float64(stage.DurationMs)/1000)
	}
	return nil
}

func LegacyStageDuration(start, end time.Time) int64 {
	if end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}
