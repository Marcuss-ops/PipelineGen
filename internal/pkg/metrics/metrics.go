package metrics

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

	// Job Metrics
	JobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_total",
		Help: "Total number of processed jobs",
	}, []string{"type", "status"})

	JobActive = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "jobs_active",
		Help: "Number of jobs currently in running state",
	}, []string{"type"})

	// Qdrant Vector Store Metrics
	QdrantSearchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "qdrant_search_duration_seconds",
		Help:    "Duration of Qdrant vector search operations",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"vector_name", "status"})

	QdrantSearchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_search_total",
		Help: "Total number of Qdrant vector search operations",
	}, []string{"vector_name", "status"})

	QdrantUpsertTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_upsert_total",
		Help: "Total number of Qdrant upsert operations",
	}, []string{"status"})

	QdrantCollectionSize = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "qdrant_collection_size",
		Help: "Number of points in the Qdrant collection",
	}, []string{"collection"})

	QdrantHealthStatus = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "qdrant_health_status",
		Help: "Qdrant health status: 1 = healthy, 0 = unreachable",
	})

	QdrantErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qdrant_errors_total",
		Help: "Total number of Qdrant operation errors",
	}, []string{"operation"})
)
