// Package observability — metadata enrichment metrics (Sept 2026).
//
// SRE surface for the metadata-enrichment pipeline that REPLACED the
// retired Step10MetricsRecorder family (metrics_step10.go was deleted
// with the Step 10 regression seam). Four Prometheus collectors,
// registered via promauto against the default registry at package init
// so /metrics exposes them on the standard scrape path:
//
//   - metadata_enrichment_total            — counter, every enrichment run
//     (synchronous inline analysis OR async outbox worker).
//   - metadata_enrichment_duration_seconds — histogram, per-run analyzer
//     duration (buckets cover a fast local Ollama round-trip up to a
//     slow remote model inference).
//   - metadata_enrichment_failures_total   — counter, runs that returned
//     an error (inline analysis failure OR worker handler error).
//   - metadata_enrichment_queue_age_seconds — histogram, age of the
//     enrichment intent when the async worker picks it up (created_at of
//     the metadata.enrich.requested event → handler start). A rising
//     tail means the outbox worker is saturated or the analyzer is
//     slower than the clip commit rate.
//
// Cardinality (bounded): all four are unlabelled (single series each) —
// the enrichment surface has exactly one logical pipeline, so a
// `mode`/`clip_id` label would add unbounded cardinality for zero SRE
// value. Operators join these series against the outbox worker metrics
// (outbox_lag_seconds) for the end-to-end enrichment SLO.
//
// Failure mode (godlike/07 NO-FAKE-AVAILABILITY): collectors are
// declared at package init via promauto.New* — a name collision panics
// at process start, and the names are namespaced to `metadata_enrichment_*`
// so collisions with outbox_*/media_* metrics are structurally impossible.
// Tests passing a fresh registry MUST re-export each metric explicitly
// (see metrics_enrichment_test.go for the pattern).
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// defaultEnrichmentDurationBuckets is the canonical Prometheus latency
// bucket set for one metadata-analysis run: sub-second (local Ollama
// with a warm model) up to 60s (cold model load or remote inference).
var defaultEnrichmentDurationBuckets = []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

var (
	// MetadataEnrichmentTotal counts every metadata-enrichment run,
	// synchronous (inline analysis in step6to9) and asynchronous (the
	// metadata.enrich.requested outbox consumer). Success rate is
	// (total - failures) / total; failures have their own counter so a
	// dashboard never has to guess from a delta.
	MetadataEnrichmentTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "metadata_enrichment_total",
		Help: "Cumulative count of metadata-enrichment runs (synchronous inline analysis and async metadata.enrich.requested outbox consumer).",
	})

	// MetadataEnrichmentDurationSeconds is the per-run analyzer duration
	// histogram. Buckets cover a fast local Ollama round-trip (0.1s)
	// up to a slow cold-model inference (60s).
	MetadataEnrichmentDurationSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "metadata_enrichment_duration_seconds",
		Help:    "Duration of one metadata-analysis run (MetadataService.AnalyzeClip / EnrichClip), synchronous and asynchronous paths.",
		Buckets: defaultEnrichmentDurationBuckets,
	})

	// MetadataEnrichmentFailuresTotal counts enrichment runs that
	// returned an error. The inline path records an analysis error
	// BEFORE the fail-closed clip abort; the async path records a
	// worker handler error that enters the lease-fenced retry/DLQ path.
	MetadataEnrichmentFailuresTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "metadata_enrichment_failures_total",
		Help: "Cumulative count of metadata-enrichment runs that returned an error (inline analysis failure or async worker handler error).",
	})

	// MetadataEnrichmentQueueAgeSeconds is the async-only histogram: age
	// of a metadata.enrich.requested intent when the outbox worker claims
	// it (now − event created_at). A rising tail indicates outbox worker
	// saturation or an analyzer slower than the clip commit rate.
	MetadataEnrichmentQueueAgeSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "metadata_enrichment_queue_age_seconds",
		Help:    "Age of a metadata.enrich.requested event when the async worker starts processing it (now - created_at).",
		Buckets: defaultEnrichmentDurationBuckets,
	})
)

// MetadataEnrichmentRecorder is the canonical Pattern-0 adapter: a
// nil-safe thin wrapper over the four package-level collectors. The
// synchronous use case path (ProcessSegmentObservabilityDeps.
// EnrichmentMetrics) and the async wiring handler both call the SAME
// collector family, so a single dashboard surface covers both
// enrichment modes (godlike/06 SSOT).
type MetadataEnrichmentRecorder struct{}

// NewMetadataEnrichmentRecorder returns the default Prometheus adapter.
func NewMetadataEnrichmentRecorder() *MetadataEnrichmentRecorder {
	return &MetadataEnrichmentRecorder{}
}

// IncEnrichmentTotal counts one enrichment run. Nil-receiver safe.
func (r *MetadataEnrichmentRecorder) IncEnrichmentTotal() {
	if r == nil {
		return
	}
	MetadataEnrichmentTotal.Inc()
}

// ObserveEnrichmentDuration records one run duration in seconds.
// Nil-receiver safe; negative durations are ignored (a broken clock
// must not corrupt the histogram).
func (r *MetadataEnrichmentRecorder) ObserveEnrichmentDuration(seconds float64) {
	if r == nil || seconds < 0 {
		return
	}
	MetadataEnrichmentDurationSeconds.Observe(seconds)
}

// IncEnrichmentFailures counts one failed enrichment run. Nil-receiver
// safe.
func (r *MetadataEnrichmentRecorder) IncEnrichmentFailures() {
	if r == nil {
		return
	}
	MetadataEnrichmentFailuresTotal.Inc()
}

// ObserveEnrichmentQueueAge records the async intent age in seconds.
// Nil-receiver safe; negative ages are ignored (clock skew between the
// committer and the worker would otherwise corrupt the histogram).
func (r *MetadataEnrichmentRecorder) ObserveEnrichmentQueueAge(seconds float64) {
	if r == nil || seconds < 0 {
		return
	}
	MetadataEnrichmentQueueAgeSeconds.Observe(seconds)
}
