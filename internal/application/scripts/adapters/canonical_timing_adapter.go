package adapters

import (
	"context"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
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

// ProjectGenerationTimings projects a canonical stage observation into the
// legacy GenerationTimings compatibility fields (no new fields, no second
// clock). The name→field mapping is fixed and additive:
//
//	"source.resolve" → SourceResolveMs
//	"script.plan"    → PlanBuildMs
//	"script.engine"  → EngineMs
//
// TotalMs is the canonical run wall time (not a stage) and PostprocessMs is
// projected per-processor via ProjectStage; both are handled by the caller.
func (a *CanonicalTimingAdapter) ProjectGenerationTimings(timings *scriptpkg.GenerationTimings, name string, stage kernobs.StageReport) {
	if timings == nil {
		return
	}
	switch name {
	case "source.resolve":
		timings.SourceResolveMs = stage.DurationMs
	case "script.plan":
		timings.PlanBuildMs = stage.DurationMs
	case "script.engine":
		timings.EngineMs = stage.DurationMs
	}
}

func LegacyStageDuration(start, end time.Time) int64 {
	if end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}
