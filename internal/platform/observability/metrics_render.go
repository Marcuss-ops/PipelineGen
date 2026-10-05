package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// metrics_render.go owns the PROMETHEUS projection of the overlay render lane.
// All labels are bounded enums/backend vocabulary; job, run and item identity
// belongs in structured analytics rows, never metric labels.
var (
	OverlayRenderTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "overlay_render_total",
		Help: "Total number of blocking overlay render attempts, by bounded outcome.",
	}, []string{"outcome"})

	OverlayRenderDurationSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "overlay_render_duration_seconds",
		Help:    "Wall time of the blocking overlay render boundary (submit + remote GPU wait).",
		Buckets: []float64{0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300, 600},
	})

	// OverlayCompletionWaitSeconds is the distinct queue terminal-wait wall
	// observed by the producer. It excludes submit, worker service and
	// post-processing, unlike the enclosing overlay_render histogram.
	OverlayCompletionWaitSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "overlay_completion_wait_seconds",
		Help:    "Producer-observed duration from render queue wait start until terminal response, by bounded outcome.",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300, 600},
	}, []string{"outcome"})

	OverlayRenderItems = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "overlay_render_items",
		Help:    "Number of semantic overlay items in the rendered plan.",
		Buckets: []float64{1, 2, 3, 5, 8, 13, 21, 34, 55},
	})

	ChrononRenderSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "chronon_render_seconds",
		Help:    "Chronon-measured render phase per certified overlay artifact, by backend.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{"backend"})

	ChrononEncodeSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "chronon_encode_seconds",
		Help:    "Worker-measured encode phase per certified overlay artifact, by backend.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{"backend"})

	ChrononFramesRenderedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "chronon_frames_rendered_total",
		Help: "Total frames produced by the GPU render lane, by backend.",
	}, []string{"backend"})

	OverlayItemRenderPoolSize = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "overlay_item_render_pool_size",
		Help: "Resolved bound on concurrent per-item overlay renders (pipelining, not GPU concurrency).",
	})

	OverlayItemRenderInFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "overlay_item_render_in_flight",
		Help: "Per-item overlay renders currently submitted and awaiting their terminal state.",
	})
)
