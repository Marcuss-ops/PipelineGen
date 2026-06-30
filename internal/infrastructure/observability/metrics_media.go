package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Video Render Metrics
	VideoRenderDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "video_render_duration_seconds",
		Help:    "Duration of video rendering jobs",
		Buckets: prometheus.DefBuckets,
	}, []string{"status", "fallback"})

	VideoRenderTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "video_render_total",
		Help: "Total number of video rendering attempts",
	}, []string{"status", "fallback"})

	// Download Metrics
	DownloadDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "download_duration_seconds",
		Help:    "Duration of media downloads",
		Buckets: prometheus.DefBuckets,
	}, []string{"source", "status"})

	DownloadTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "download_total",
		Help: "Total number of media downloads",
	}, []string{"source", "status"})

	// Media Index Pipeline Metrics
	MediaIndexSuccessTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_index_success_total",
		Help: "Total number of successfully indexed media assets, by source",
	}, []string{"source"})

	MediaIndexFailureTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_index_failure_total",
		Help: "Total number of failed media indexing attempts, by source",
	}, []string{"source"})

	MediaIndexRetryTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_index_retry_total",
		Help: "Total number of media indexing retries, by source",
	}, []string{"source"})

	MediaIndexAttemptsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_index_attempts_total",
		Help: "Total number of asset.index.* handler entries, by event_type",
	}, []string{"event_type"})

	MediaIndexSupersededTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_index_superseded_total",
		Help: "Total number of asset.index.requested events short-circuited by source_version supersede, by event_type",
	}, []string{"event_type"})

	MediaIndexDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "media_index_duration_seconds",
		Help:    "Duration of asset.index.* handler invocations by outcome",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
	}, []string{"event_type", "outcome"})

	StaleAssets = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "media_index_stale_assets",
		Help: "Number of media_assets rows stuck in non-terminal index states past the freshness threshold, by source and state",
	}, []string{"source", "state"})

	EmbeddingServerLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "embedding_server_duration_seconds",
		Help:    "Duration of calls to the external embedding server, by endpoint and outcome",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
	}, []string{"endpoint", "outcome"})

	// Media Curator Metrics
	MediaCuratorSearchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mediacurator_search_total",
		Help: "Total number of MediaCurator searches, partitioned by backend (qdrant, like, error)",
	}, []string{"backend"})

	MediaCuratorSearchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "mediacurator_search_duration_seconds",
		Help:    "Duration of MediaCurator search operations by backend",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"backend"})

	// Dedup Metrics
	DedupHits = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "clip_dedup_hits_total",
		Help: "Total number of clip registrations skipped because the clip was already present (dedup hit), partitioned by source and trigger",
	}, []string{"source", "trigger"})

	DedupMisses = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "clip_dedup_misses_total",
		Help: "Total number of clip registration dedup checks that found no duplicate, partitioned by source and trigger",
	}, []string{"source", "trigger"})

	DedupMerged = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "clip_dedup_merged_total",
		Help: "Total number of duplicate clips merged/soft-deleted by the post-hoc dedup sweeper",
	}, []string{"source", "reason"})
)
