package localization

// reuse_metrics.go owns the observable side of the localized-render reuse
// cache. Reuse is an optimization, so its effect must be measurable instead of
// inferred from a missing render or a log line: a hit and an empty plan looked
// identical in run reports before these counters existed.

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// LocalizedRenderReuseTotal counts every lookup by result. A miss carries
	// a bounded reason so a cache that is never hit can be diagnosed without
	// reading logs.
	LocalizedRenderReuseTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "localized_render_reuse_total",
		Help: "Localized render reuse lookups by result (hit/miss) and miss reason.",
	}, []string{"result", "reason"})

	// LocalizedRenderReuseVerifySeconds measures the local byte verification
	// (stat + SHA-256) that makes a hit trustworthy.
	LocalizedRenderReuseVerifySeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "localized_render_reuse_verify_seconds",
		Help:    "Time spent verifying cached localized render bytes before reuse.",
		Buckets: prometheus.DefBuckets,
	})
)

func recordReuseHit() {
	LocalizedRenderReuseTotal.WithLabelValues("hit", "").Inc()
}

func recordReuseMiss(reason string) {
	LocalizedRenderReuseTotal.WithLabelValues("miss", reason).Inc()
}
