// Package observability — metrics_step10.go: typed-port adapter for the
// YouTube Step 10 partial-state metric.
//
// PR-PY-STEP10-FAIL-LOG-OBSEVE-PARITY (July 2026): the application-layer
// ProcessYouTubeSegmentUseCase calls this adapter through the
// youtubeports.Step10MetricsRecorder interface; the adapter wraps the
// Prometheus counter `transcript_metadata_step10_fail_after_clip_total`
// (defined in metrics_jobs.go under "YouTube Pipeline Metrics").
//
// godlike/06 SSOT (one canonical owner per fact): the
// Step10MetricsAdapter is the SOLE canonical writer of the
// transcript_metadata_step10_fail_after_clip_total counter on the
// application-side wiring. The counter definition itself lives in
// metrics_jobs.go (alongside the other YouTube Pipeline Metrics) so
// the metric remains co-located with its peers.
//
// godlike/07 typed-error contract: the failureCode parameter MUST be
// the stringified FailureCode constant (e.g.
// string(usecase.FailureCodeMetadataFailed) = "metadata_failed") so
// dashboard queries can join against the typed-error taxonomy in
// internal/application/youtube/usecase/errors.go without string
// parsing. Passing a free-form string would orphan the metric label
// from the canonical taxonomy.
package observability

// Step10MetricsAdapter is the typed-port adapter that satisfies
// youtubeports.Step10MetricsRecorder. The composition root wires it
// via NewStep10MetricsAdapter().
//
// The adapter is a zero-size struct (no fields) because the wrapped
// counter is a package-level singleton in metrics_jobs.go; there is
// no per-instance state to carry.
type Step10MetricsAdapter struct{}

// NewStep10MetricsAdapter returns the canonical Step 10 metrics
// adapter. The composition root invokes this once during process
// SegmentDeps wiring.
func NewStep10MetricsAdapter() *Step10MetricsAdapter {
	return &Step10MetricsAdapter{}
}

// IncStep10FailAfterClip increments the
// transcript_metadata_step10_fail_after_clip_total counter with the
// given failure_code label. failureCode MUST be the stringified
// FailureCode constant — see the package doc for the typed-error
// contract.
func (*Step10MetricsAdapter) IncStep10FailAfterClip(failureCode string) {
	Step10FailAfterClipTotal.WithLabelValues(failureCode).Inc()
}
