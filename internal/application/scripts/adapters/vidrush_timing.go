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

func observeVidRushProviderDuration(metrics any, provider string, elapsed time.Duration) {
	if timing, ok := metrics.(VidRushTimingMetrics); ok && timing != nil {
		timing.ObserveProviderDuration(provider, elapsed.Seconds())
	}
}

// measureVidRushProvider makes the canonical run observation the only timer
// on worker executions. The Prometheus timing hook remains only for callers
// that intentionally execute the processor without a bound Run (legacy
// callers and unit fixtures).
func measureVidRushProvider(ctx context.Context, metrics any, info kernobs.OperationInfo, fn func(context.Context) error) error {
	if kernobs.FromContext(ctx) != nil {
		return kernobs.MeasureOperation(ctx, info, fn)
	}
	started := time.Now()
	err := fn(ctx)
	observeVidRushProviderDuration(metrics, info.Provider+"_"+string(info.Operation), time.Since(started))
	return err
}
