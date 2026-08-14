package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

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

	// ── Performance metrics (VidRush battery, July 2026) ──────────────
	// Labels are intentionally limited: no job_id, segment_id, asset_id,
	// query, title, or user_id. Dynamic values stay in structured logs.

	vidrushJobDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "vidrush_job_duration_seconds",
		Help:    "End-to-end job duration from dispatch to terminal status.",
		Buckets: []float64{1, 5, 10, 30, 60, 120, 300, 600, 900},
	}, []string{"scenario", "cache_mode"})

	vidrushQueueWait = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "vidrush_queue_wait_seconds",
		Help:    "Time spent waiting in queue before processing starts.",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
	}, []string{"scenario"})

	vidrushProcessorDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "vidrush_processor_duration_seconds",
		Help:    "Duration of each postprocessor step.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30, 60},
	}, []string{"processor"})

	vidrushProviderDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "vidrush_provider_duration_seconds",
		Help:    "Duration of provider calls (search, download, etc.).",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120},
	}, []string{"provider"})

	vidrushSegmentsPerJob = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "vidrush_segments_per_job",
		Help:    "Number of segments produced per job.",
		Buckets: []float64{1, 2, 3, 5, 8, 12, 16, 20},
	})

	vidrushCandidatesPerSegment = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "vidrush_candidates_per_segment",
		Help:    "Number of asset candidates per segment.",
		Buckets: []float64{0, 1, 2, 3, 5, 8, 10, 15},
	}, []string{"provider"})

	vidrushJobFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "vidrush_job_failures_total",
		Help: "Total number of failed VidRush jobs.",
	}, []string{"scenario", "stage"})

	vidrushRetries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "vidrush_retries_total",
		Help: "Total number of provider retries.",
	}, []string{"provider", "reason"})

	vidrushBindingRatio = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "vidrush_binding_ratio",
		Help: "Ratio of successfully bound segments to total segments (0.0–1.0).",
	})

	vidrushUnresolvedRatio = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "vidrush_unresolved_ratio",
		Help: "Ratio of unresolved segments to total segments (0.0–1.0).",
	})

	// ── Incremental scene pipeline metrics (SceneCommitted → enrichment →
	// barrier). Labels are intentionally absent: dynamic run/scene/segment ids
	// stay in structured logs, never in metric labels.

	vidrushSceneCommitted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "vidrush_scene_committed_total",
		Help: "Stable scenes committed to the incremental VidRush pipeline.",
	})

	vidrushSceneEnrichmentStarted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "vidrush_scene_enrichment_started_total",
		Help: "Scene enrichments that began (entities → providers → materialize).",
	})

	vidrushSceneEnrichmentCompleted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "vidrush_scene_enrichment_completed_total",
		Help: "Scene enrichments that completed (success or error).",
	})

	vidrushSceneEnrichmentDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "vidrush_scene_enrichment_duration_seconds",
		Help:    "Wall-clock duration of a single scene enrichment.",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60},
	})

	vidrushGenerationOverlap = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "vidrush_generation_overlap_seconds",
		Help:    "Wall-clock overlap between scene generation and VidRush enrichment. Positive values prove enrichment began before generation finished.",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120},
	})

	vidrushBarrierWait = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "vidrush_barrier_wait_seconds",
		Help:    "Wall-clock time the final VidRush barrier waited for still-running enrichments.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30},
	})

	vidrushStaleResults = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "vidrush_stale_results_total",
		Help: "Enrichment results discarded by stale-result fencing.",
	})

	vidrushInflightSegments = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "vidrush_inflight_segments",
		Help: "Number of scene enrichments currently in flight.",
	})
)

func init() {
	prometheus.MustRegister(
		vidrushSegmentsTotal, vidrushExtractionHits, vidrushExtractionMisses,
		vidrushAssetHits, vidrushAssetMisses, vidrushProviderRequests, vidrushProviderFailures,
		vidrushBindingsTotal, vidrushUnresolved,
		// Performance metrics
		vidrushJobDuration, vidrushQueueWait, vidrushProcessorDuration,
		vidrushProviderDuration, vidrushSegmentsPerJob, vidrushCandidatesPerSegment,
		vidrushJobFailures, vidrushRetries,
		vidrushBindingRatio, vidrushUnresolvedRatio,
		// Incremental scene pipeline metrics
		vidrushSceneCommitted, vidrushSceneEnrichmentStarted, vidrushSceneEnrichmentCompleted,
		vidrushSceneEnrichmentDuration, vidrushGenerationOverlap, vidrushBarrierWait,
		vidrushStaleResults, vidrushInflightSegments,
	)
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

// ── Performance metric helpers ────────────────────────────────────────

func (a *VidRushMetricsAdapter) ObserveJobDuration(scenario, cacheMode string, seconds float64) {
	vidrushJobDuration.WithLabelValues(scenario, cacheMode).Observe(seconds)
}
func (a *VidRushMetricsAdapter) ObserveQueueWait(scenario string, seconds float64) {
	vidrushQueueWait.WithLabelValues(scenario).Observe(seconds)
}
func (a *VidRushMetricsAdapter) ObserveProcessorDuration(processor string, seconds float64) {
	vidrushProcessorDuration.WithLabelValues(processor).Observe(seconds)
}
func (a *VidRushMetricsAdapter) ObserveProviderDuration(provider string, seconds float64) {
	vidrushProviderDuration.WithLabelValues(provider).Observe(seconds)
}
func (a *VidRushMetricsAdapter) ObserveSegmentsPerJob(count float64) {
	vidrushSegmentsPerJob.Observe(count)
}
func (a *VidRushMetricsAdapter) ObserveCandidatesPerSegment(provider string, count float64) {
	vidrushCandidatesPerSegment.WithLabelValues(provider).Observe(count)
}
func (a *VidRushMetricsAdapter) IncJobFailure(scenario, stage string) {
	vidrushJobFailures.WithLabelValues(scenario, stage).Inc()
}
func (a *VidRushMetricsAdapter) IncRetry(provider, reason string) {
	vidrushRetries.WithLabelValues(provider, reason).Inc()
}
func (a *VidRushMetricsAdapter) SetBindingRatio(ratio float64) {
	vidrushBindingRatio.Set(ratio)
}
func (a *VidRushMetricsAdapter) SetUnresolvedRatio(ratio float64) {
	vidrushUnresolvedRatio.Set(ratio)
}

// ── Incremental scene pipeline metrics (VidRushMetrics port) ─────────────

func (*VidRushMetricsAdapter) SceneCommitted() { vidrushSceneCommitted.Inc() }
func (*VidRushMetricsAdapter) EnrichmentStarted() {
	vidrushSceneEnrichmentStarted.Inc()
	vidrushInflightSegments.Inc()
}
func (*VidRushMetricsAdapter) EnrichmentCompleted(duration time.Duration) {
	vidrushSceneEnrichmentCompleted.Inc()
	vidrushSceneEnrichmentDuration.Observe(duration.Seconds())
	vidrushInflightSegments.Dec()
}
func (*VidRushMetricsAdapter) BarrierWait(seconds float64) {
	vidrushBarrierWait.Observe(seconds)
}
func (*VidRushMetricsAdapter) GenerationOverlap(seconds float64) {
	vidrushGenerationOverlap.Observe(seconds)
}
func (*VidRushMetricsAdapter) StaleResult() { vidrushStaleResults.Inc() }
