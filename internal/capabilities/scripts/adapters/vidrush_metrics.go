package adapters

// VidRushTimingMetrics is the optional duration-observation surface shared by
// the VidRush processors and canonical timing adapter. Counter-only metrics
// implementations may omit it.
type VidRushTimingMetrics interface {
	ObserveProcessorDuration(processor string, seconds float64)
	ObserveProviderDuration(provider string, seconds float64)
}
