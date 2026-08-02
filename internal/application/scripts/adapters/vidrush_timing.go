package adapters

import "time"

// VidRushTimingMetrics is the optional timing extension of the bounded
// VidRush counter port. Keeping it separate preserves compatibility with
// lightweight unit-test metrics implementations while allowing the concrete
// Prometheus adapter to expose provider and processor latency.
type VidRushTimingMetrics interface {
	ObserveProcessorDuration(processor string, seconds float64)
	ObserveProviderDuration(provider string, seconds float64)
}

func observeVidRushProcessorDuration(metrics any, processor string, elapsed time.Duration) {
	if timing, ok := metrics.(VidRushTimingMetrics); ok && timing != nil {
		timing.ObserveProcessorDuration(processor, elapsed.Seconds())
	}
}

func observeVidRushProviderDuration(metrics any, provider string, elapsed time.Duration) {
	if timing, ok := metrics.(VidRushTimingMetrics); ok && timing != nil {
		timing.ObserveProviderDuration(provider, elapsed.Seconds())
	}
}
