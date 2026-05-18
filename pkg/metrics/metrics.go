package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Tachyon Metrics
	TachyonRenderDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "tachyon_render_duration_seconds",
		Help:    "Duration of Tachyon rendering jobs",
		Buckets: prometheus.DefBuckets,
	}, []string{"status", "fallback"})

	TachyonRenderTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "tachyon_render_total",
		Help: "Total number of Tachyon rendering attempts",
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
)
