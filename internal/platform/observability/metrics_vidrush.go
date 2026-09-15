package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	// Only metrics with a LIVE observer are declared and registered here. A
	// collector registered with Prometheus reports a zero-valued series from
	// the first scrape, so an event nobody records is indistinguishable from an
	// event that never happens: a dashboard cannot tell "no failures" from "the
	// failure counter was never wired". The former segment/extraction/binding/
	// unresolved counters and the job-duration/queue-wait/segments-per-job/
	// candidates-per-segment/retry/ratio instruments were declared for a VidRush
	// battery whose emitters no longer exist — no code path wrote them and no
	// caller read them — so they were removed rather than kept as series that
	// assert a reality nobody measures.
	vidrushAssetHits        = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vidrush_asset_cache_hits_total", Help: "VidRush asset cache hits."}, []string{"provider"})
	vidrushAssetMisses      = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vidrush_asset_cache_misses_total", Help: "VidRush asset cache misses."}, []string{"provider"})
	vidrushProviderRequests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vidrush_provider_requests_total", Help: "VidRush provider requests."}, []string{"provider"})
	vidrushProviderFailures = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vidrush_provider_failures_total", Help: "VidRush provider failures."}, []string{"provider"})

	// ── Persistent PERSON entity-image catalog metrics ────────────────
	// These metrics intentionally have no dynamic labels. Entity names,
	// URLs and queries belong in structured logs, never in Prometheus labels.
	entityImageCatalogHits          = prometheus.NewCounter(prometheus.CounterOpts{Name: "entity_image_catalog_hits_total", Help: "Persistent PERSON image-catalog lookups with a sufficient usable pool."})
	entityImageCatalogMisses        = prometheus.NewCounter(prometheus.CounterOpts{Name: "entity_image_catalog_misses_total", Help: "Persistent PERSON image-catalog lookups without a sufficient usable pool."})
	entityImageCatalogRefreshes     = prometheus.NewCounter(prometheus.CounterOpts{Name: "entity_image_catalog_refresh_total", Help: "PERSON image-catalog refreshes that call the image provider."})
	entityImageCatalogBrokenURLs    = prometheus.NewCounter(prometheus.CounterOpts{Name: "entity_image_catalog_url_broken_total", Help: "PERSON image-catalog URLs that failed acquisition or verification."})
	entityImageCatalogProviderCalls = prometheus.NewCounter(prometheus.CounterOpts{Name: "entity_image_catalog_provider_calls_total", Help: "External image-provider calls made for PERSON catalog population or refresh."})
	entityImageCatalogDriveReuses   = prometheus.NewCounter(prometheus.CounterOpts{Name: "entity_image_catalog_drive_reuse_total", Help: "PERSON images reused from verified Drive materializations."})
	entityImageCatalogNewDownloads  = prometheus.NewCounter(prometheus.CounterOpts{Name: "entity_image_catalog_new_download_total", Help: "PERSON image downloads performed because no verified Drive materialization was reusable."})

	// ── Provider/processor timing metrics (VidRush) ──────────────────
	// Labels are intentionally limited: no job_id, segment_id, asset_id,
	// query, title, or user_id. Dynamic values stay in structured logs.

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

	entityImageCatalogLookupDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "entity_image_catalog_lookup_seconds",
		Help:    "Duration of persistent PERSON image-catalog lookups.",
		Buckets: []float64{0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 2},
	})

	entityImageCatalogMaterializationDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "entity_image_catalog_materialization_seconds",
		Help:    "Duration of PERSON image acquisition, verification and finalization.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30, 60},
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
		vidrushAssetHits, vidrushAssetMisses, vidrushProviderRequests, vidrushProviderFailures,
		entityImageCatalogHits, entityImageCatalogMisses, entityImageCatalogRefreshes,
		entityImageCatalogBrokenURLs, entityImageCatalogProviderCalls,
		entityImageCatalogDriveReuses, entityImageCatalogNewDownloads,
		entityImageCatalogLookupDuration, entityImageCatalogMaterializationDuration,
		// Provider/processor timing metrics
		vidrushProcessorDuration, vidrushProviderDuration,
		// Incremental scene pipeline metrics
		vidrushSceneCommitted, vidrushSceneEnrichmentStarted, vidrushSceneEnrichmentCompleted,
		vidrushSceneEnrichmentDuration, vidrushGenerationOverlap, vidrushBarrierWait,
		vidrushStaleResults, vidrushInflightSegments,
	)
}

type VidRushMetricsAdapter struct{}

func NewVidRushMetricsAdapter() *VidRushMetricsAdapter { return &VidRushMetricsAdapter{} }
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

// ── Persistent PERSON entity-image catalog metrics ────────────────────

func (*VidRushMetricsAdapter) IncEntityImageCatalogLookup(hit bool) {
	if hit {
		entityImageCatalogHits.Inc()
		return
	}
	entityImageCatalogMisses.Inc()
}
func (*VidRushMetricsAdapter) IncEntityImageCatalogRefresh() {
	entityImageCatalogRefreshes.Inc()
}
func (*VidRushMetricsAdapter) IncEntityImageCatalogURLBroken() {
	entityImageCatalogBrokenURLs.Inc()
}
func (*VidRushMetricsAdapter) IncEntityImageCatalogProviderCall() {
	entityImageCatalogProviderCalls.Inc()
}
func (*VidRushMetricsAdapter) ObserveEntityImageCatalogLookup(seconds float64) {
	entityImageCatalogLookupDuration.Observe(seconds)
}
func (*VidRushMetricsAdapter) ObserveEntityImageCatalogMaterialization(seconds float64) {
	entityImageCatalogMaterializationDuration.Observe(seconds)
}
func (*VidRushMetricsAdapter) IncEntityImageCatalogDriveReuse() {
	entityImageCatalogDriveReuses.Inc()
}
func (*VidRushMetricsAdapter) IncEntityImageCatalogNewDownload() {
	entityImageCatalogNewDownloads.Inc()
}

// ── Provider/processor timing helpers ─────────────────────────────────

func (a *VidRushMetricsAdapter) ObserveProcessorDuration(processor string, seconds float64) {
	vidrushProcessorDuration.WithLabelValues(processor).Observe(seconds)
}
func (a *VidRushMetricsAdapter) ObserveProviderDuration(provider string, seconds float64) {
	vidrushProviderDuration.WithLabelValues(provider).Observe(seconds)
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
