package adapters

import (
	"context"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// VidRushTimingMetrics is the optional timing extension of the bounded
// VidRush counter port. Keeping it separate preserves compatibility with
// lightweight unit-test metrics implementations while allowing the concrete
// Prometheus adapter to expose provider and processor latency.
type VidRushTimingMetrics interface {
	ObserveProcessorDuration(processor string, seconds float64)
	ObserveProviderDuration(provider string, seconds float64)
}

// measureVidRushProvider makes the canonical run observation the only timer
// on worker executions. Calls without a bound run remain unmeasured rather
// than creating a second legacy Prometheus timing path.
func measureVidRushProvider(ctx context.Context, metrics any, info kernobs.OperationInfo, fn func(context.Context) error) error {
	if kernobs.FromContext(ctx) != nil {
		return kernobs.MeasureOperation(ctx, info, fn)
	}
	// Standalone processor tests and legacy callers may not have a run
	// context. Still use the canonical operation measurement and project its
	// single result to the compatibility VidRush metric.
	run := kernobs.NewRunObserver(nil).StartRun(ctx, kernobs.RunInfo{AttemptID: "standalone"})
	callCtx := kernobs.WithRun(ctx, run)
	started := time.Now()
	err := kernobs.MeasureOperation(callCtx, info, fn)
	if timing, ok := metrics.(VidRushTimingMetrics); ok {
		report := run.Report()
		if len(report.Operations) > 0 {
			timing.ObserveProviderDuration(info.Provider+"_search", float64(report.Operations[len(report.Operations)-1].DurationMs)/1000)
		} else {
			timing.ObserveProviderDuration(info.Provider+"_search", time.Since(started).Seconds())
		}
	}
	return err
}
