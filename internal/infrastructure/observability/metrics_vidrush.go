package observability

import "github.com/prometheus/client_golang/prometheus"

var (
	vidrushSegmentsTotal    = prometheus.NewCounter(prometheus.CounterOpts{Name: "vidrush_segments_total", Help: "VidRush segments processed."})
	vidrushExtractionHits   = prometheus.NewCounter(prometheus.CounterOpts{Name: "vidrush_extraction_cache_hits_total", Help: "VidRush extraction cache hits."})
	vidrushExtractionMisses = prometheus.NewCounter(prometheus.CounterOpts{Name: "vidrush_extraction_cache_misses_total", Help: "VidRush extraction cache misses."})
	vidrushAssetHits        = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vidrush_asset_cache_hits_total", Help: "VidRush asset cache hits."}, []string{"provider"})
	vidrushAssetMisses      = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vidrush_asset_cache_misses_total", Help: "VidRush asset cache misses."}, []string{"provider"})
	vidrushProviderRequests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vidrush_provider_requests_total", Help: "VidRush provider requests."}, []string{"provider"})
	vidrushProviderFailures = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vidrush_provider_failures_total", Help: "VidRush provider failures."}, []string{"provider"})
	vidrushBindingsTotal    = prometheus.NewCounter(prometheus.CounterOpts{Name: "vidrush_bindings_total", Help: "VidRush bindings finalized."})
	vidrushUnresolved       = prometheus.NewCounter(prometheus.CounterOpts{Name: "vidrush_unresolved_segments_total", Help: "VidRush segments without a valid asset."})
)

func init() {
	prometheus.MustRegister(vidrushSegmentsTotal, vidrushExtractionHits, vidrushExtractionMisses, vidrushAssetHits, vidrushAssetMisses, vidrushProviderRequests, vidrushProviderFailures, vidrushBindingsTotal, vidrushUnresolved)
}

type VidRushMetricsAdapter struct{}

func NewVidRushMetricsAdapter() *VidRushMetricsAdapter { return &VidRushMetricsAdapter{} }
func (*VidRushMetricsAdapter) IncSegments()            { vidrushSegmentsTotal.Inc() }
func (*VidRushMetricsAdapter) IncExtractionCache(hit bool) {
	if hit {
		vidrushExtractionHits.Inc()
	} else {
		vidrushExtractionMisses.Inc()
	}
}
func (*VidRushMetricsAdapter) IncAssetCache(provider string, hit bool) {
	if hit {
		vidrushAssetHits.WithLabelValues(provider).Inc()
	} else {
		vidrushAssetMisses.WithLabelValues(provider).Inc()
	}
}
func (*VidRushMetricsAdapter) IncProviderRequest(provider string) {
	vidrushProviderRequests.WithLabelValues(provider).Inc()
}
func (*VidRushMetricsAdapter) IncProviderFailure(provider string) {
	vidrushProviderFailures.WithLabelValues(provider).Inc()
}
func (*VidRushMetricsAdapter) IncBinding()           { vidrushBindingsTotal.Inc() }
func (*VidRushMetricsAdapter) IncUnresolvedSegment() { vidrushUnresolved.Inc() }
