package adapters

import (
	"context"
	"strings"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func measureVidRushProvider(ctx context.Context, metrics any, info kernobs.OperationInfo, fn func(context.Context) error) error {
	if kernobs.FromContext(ctx) != nil {
		return kernobs.MeasureOperation(ctx, info, fn)
	}
	run := kernobs.NewRunObserver(nil).StartRun(ctx, kernobs.RunInfo{AttemptID: "standalone"})
	started := time.Now()
	err := kernobs.MeasureOperation(kernobs.WithRun(ctx, run), info, fn)
	if timing, ok := metrics.(VidRushTimingMetrics); ok {
		report := run.Report()
		seconds := time.Since(started).Seconds()
		if len(report.Operations) > 0 {
			seconds = float64(report.Operations[len(report.Operations)-1].DurationMs) / 1000
		}
		timing.ObserveProviderDuration(info.Provider+"_search", seconds)
	}
	return err
}

// SegmentTimingBudget is the canonical visual timing input. Voiceover timing
// has precedence over scene timing, which has precedence over text estimation.
type SegmentTimingBudget = scriptpkg.VisualTimingBudget

func ResolveSegmentTimingBudget(segment scriptpkg.VidRushSegmentResult, plan *scriptpkg.ResolvedGenerationPlan) SegmentTimingBudget {
	if duration := voiceoverDurationMs(segment); duration > 0 {
		return SegmentTimingBudget{SegmentID: segment.SegmentID, DurationMs: duration, Source: "voiceover"}
	}
	if duration := segmentSceneDurationMs(segment); duration > 0 {
		return SegmentTimingBudget{SegmentID: segment.SegmentID, DurationMs: duration, Source: "scene"}
	}
	if duration := estimatedSegmentDurationMs(segment, plan); duration > 0 {
		return SegmentTimingBudget{SegmentID: segment.SegmentID, DurationMs: duration, Source: "estimated"}
	}
	return SegmentTimingBudget{SegmentID: segment.SegmentID}
}

func segmentDurationBudgetMs(segment scriptpkg.VidRushSegmentResult, plan *scriptpkg.ResolvedGenerationPlan) (int64, string) {
	budget := ResolveSegmentTimingBudget(segment, plan)
	return budget.DurationMs, budget.Source
}

func voiceoverDurationMs(segment scriptpkg.VidRushSegmentResult) int64 {
	if segment.Assets.PrimaryVideo != nil && segment.Assets.PrimaryVideo.DurationMs > 0 {
		return segment.Assets.PrimaryVideo.DurationMs
	}
	return 0
}

func segmentSceneDurationMs(segment scriptpkg.VidRushSegmentResult) int64 {
	for _, candidate := range segment.Assets.Candidates {
		if candidate.DurationMs > 0 && !strings.EqualFold(candidate.Provider, scriptpkg.VidRushProviderYouTube) {
			return candidate.DurationMs
		}
	}
	return 0
}
func estimatedSegmentDurationMs(segment scriptpkg.VidRushSegmentResult, _ *scriptpkg.ResolvedGenerationPlan) int64 {
	words := len(strings.Fields(segment.Text))
	if words == 0 {
		return 0
	}
	duration := int64(words) * 400
	if duration < 4000 {
		return 4000
	}
	return duration
}
