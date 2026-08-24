package search

import (
	"time"

	reconciler "github.com/Marcuss-ops/PipelineGen/internal/capabilities/reconciliation"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// PromMetricsAdapter is the canonical wiring of reconciler.Metrics
// to internal/platform/observability vars (QDRANT-005C).
//
// The adapter constructs nothing — all underlying metrics are
// package-level promauto collectors, so the struct is empty and can
// be passed by value (no pointer coupling required).
//
// Production wire-up: pass PromMetricsAdapter{} directly in
// reconciler.ServiceDeps.Metrics.
//
// To disable metrics entirely (e.g. isolated tests that don't want
// prom counters exposed globally) pass noopMetricsAdapter{} (test
// local) OR leave Deps.Metrics nil — NewServiceFromDeps falls back to
// the in-package noopMetrics default.
//
// Idempotent: every method is safe to call concurrently because all
// prometheus CounterVec / HistogramVec / Gauge operations are
// internally synchronised by client_golang.
type PromMetricsAdapter struct{}

// Compile-time assertion that PromMetricsAdapter satisfies
// reconciler.Metrics — catches signature drift at compile time.
var _ reconciler.Metrics = PromMetricsAdapter{}

// RecordFindings iterates counts (9 ClassificationKind values) and
// bumps findings_total{kind=...} for every non-zero entry. All 9
// ClassificationKind values are pre-incremented by 0 (no observable
// effect) so PromQL `sum by (kind)` returns zero cleanly when no
// findings happened.
//
// Zero-value counts: skipped (Pre-init done lazily by Prometheus so
// we don't pay the label-cardinality cost for absent kinds).
func (PromMetricsAdapter) RecordFindings(counts map[reconciler.ClassificationKind]int) {
	for k, n := range counts {
		if n <= 0 {
			continue
		}
		observability.ReconcilerFindingsTotal.WithLabelValues(string(k)).Add(float64(n))
	}
}

// RecordVersionMismatchPerChannel iterates per-channel counts and
// bumps version_mismatch_per_channel_total{channel=...} per entry.
//
// NOTE: this metric complements (does NOT replace) findings_total{kind=
// "version_stale"}. The pair relation is:
//
//	findings_total{kind="version_stale"} SUM = SUM over channel of
//	version_mismatch_per_channel_total{channel=...}
func (PromMetricsAdapter) RecordVersionMismatchPerChannel(perChannel map[string]int) {
	for ch, n := range perChannel {
		if n <= 0 {
			continue
		}
		observability.ReconcilerVersionMismatchPerChannel.WithLabelValues(ch).Add(float64(n))
	}
}

// RecordDispatch bumps dispatches_total{action=...} by n. Bumps n
// times so a single Apply-mode run with N repairs of the same action
// emits N. Action label values: "reindex" | "delete" | "payload_strip".
func (PromMetricsAdapter) RecordDispatch(action string, n int) {
	if n <= 0 {
		return
	}
	observability.ReconcilerDispatchesTotal.WithLabelValues(action).Add(float64(n))
}

// RecordLegacyKeyStripped bumps legacy_cleaned_total{legacy_key=...}
// by n. legacy_key label values: "status" | "drive_link" | "local_path".
func (PromMetricsAdapter) RecordLegacyKeyStripped(legacyKey string, n int) {
	if n <= 0 {
		return
	}
	observability.PayloadLegacyCleanedTotal.WithLabelValues(legacyKey).Add(float64(n))
}

// RecordErrors bumps errors_total by n. n typically equals
// len(report.Errors) at run end.
func (PromMetricsAdapter) RecordErrors(n int) {
	if n <= 0 {
		return
	}
	observability.ReconcilerErrorsTotal.Add(float64(n))
}

// RecordRunComplete:
//  1. Sets last_success_timestamp_seconds to time.Now().Unix() so
//     "no reconcile in N minutes" alerts can fire.
//  2. Observes duration_seconds in the histogram labelled by mode.
//
// Order of operations matters for dashboards that compare the two:
// setting the timestamp AFTER histogram observation prevents a flake
// where the histogram points are merged before the timestamp settles.
func (PromMetricsAdapter) RecordRunComplete(mode string, durationSeconds float64) {
	observability.ReconcilerDuration.WithLabelValues(mode).Observe(durationSeconds)
	observability.ReconcilerLastSuccess.Set(float64(time.Now().Unix()))
}
