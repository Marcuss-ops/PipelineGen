// Package monitor — MetricsRecorder port.
//
// FASE 3.7 Commit 2 (2026-07-04): zero-infra-imports commitment
// for the Prometheus counters + histogram previously consumed
// directly via the `internal/platform/observability`
// package-level vars. The monitor package now declares its own
// narrow MetricsRecorder port + calls it through m.metrics on the
// ChannelMonitor struct. The infrastructure-side adapter
// (ObservabilityMetricsRecorder, in
// internal/platform/observability/metrics_adapter.go) is
// wired at the composition root in internal/app/lifecycle.go.
//
// Pattern 0 + godlike/06 (one owner per fact): monitor OWNS the
// semantic shape of "what counters does the channel monitor emit"
// (IncVideosChecked / IncVideosWithSegments / AddSegmentsFound /
// ObserveSegmentsPerVideo). observability OWNS the Prometheus
// client_golang machinery (CounterVec + HistogramVec, label
// semantics, default collector registration). The composition
// root bridges the two via the ObservabilityMetricsRecorder
// adapter — no monitor-side call site imports Prometheus directly,
// no observability-side caller knows about monitor.
//
// Drift guard: changes to the Prometheus CounterVec label set
// (e.g. adding a "region" label) flow through the adapter
// constructor signature — a drift between the adapter fields
// and the production CounterVec declarations is a build-time
// failure, not a runtime panic.
package assets

// MetricsRecorder is the canonical Prometheus-shaped counter/histogram
// surface for the channel monitor. The interface accepts the typed
// shapes the monitor callers actually use (string handle + int count);
// the adapter translates them to the Prometheus idiom (CounterVec with
// label values) at composition time.
//
// Methods enforce two rules from the legacy
// `internal/platform/observability` direct-usage path:
//   - IncVideosChecked(handle): one Inc per analyzer-call channel
//     (pre-Commit-2 call site: `metrics.ChannelMonitorVideosChecked
//     .WithLabelValues(handle).Inc()`).
//   - IncVideosWithSegments + AddSegmentsFound: both fire on the
//     same successful-analyzer path, both stamped with the same
//     label (pre-Commit-2 contiguous metrics call pair at lines
//     117/118 of analyzer.go).
//   - ObserveSegmentsPerVideo: one Observe(segCount) per analyzer
//     call regardless of outcome — including the empty-segments
//     soft-skip path (segCount=0). The histogram's zero-bucket
//     captures the empty case; pre-Commit-2 behaviour preserved.
type MetricsRecorder interface {
	// IncVideosChecked increments the videos-checked counter for a
	// single channel. Caller-supplied channel is treated as the
	// Prometheus label value verbatim (no normalization, no
	// sanitization); production callers always supply
	// `extractChannelHandle(channel.ChannelURL)` with
	// empty-string → "unknown" substitution per the pre-Commit-2
	// analyzer.go guard. The parameter name `channel` matches
	// the Prometheus label key exactly (the 4 counters + the
	// histogram all label by "channel"); consistent with the
	// other 3 port methods which also accept `channel`.
	IncVideosChecked(channel string)

	// IncVideosWithSegments increments the videos-with-segments
	// counter for a single channel label. Fires only on the
	// successful-analyzer path (analysis.Segments non-empty).
	IncVideosWithSegments(channel string)

	// AddSegmentsFound adds the segment-count delta to the
	// segments-found counter. Fires on the same path as
	// IncVideosWithSegments; both share the channel label.
	AddSegmentsFound(channel string, n int)

	// ObserveSegmentsPerVideo records a single histogram
	// observation. Fires unconditionally on every analyzer call
	// (including len(segments)==0 — zeros enter the histogram's
	// first bucket, which is configured for explicit zero
	// capture).
	ObserveSegmentsPerVideo(channel string, n int)
}

// NoopMetricsRecorder is the safe zero-value default for the
// MetricsRecorder port. Installed by NewChannelMonitor whenever
// the caller-supplied CompositionDeps.MetricsRecorder is nil —
// the production composition (lifecycle.go) wires the concrete
// observability adapter; the no-op is for tests that construct
// the ChannelMonitor by CompositionDeps without a metrics port
// (or by struct-literal, which still compiles because the field
// is just nil).
//
// All methods are no-ops; nil-receiver and nil-default safe.
// Distinct from the observability "Blackhole" prometheus pattern:
// a `mh.metrics = prometheus.NewCounterVec(...)` would register
// a counter that produces observations visible to operators
// inspecting label cardinality in production — preserving the
// pre-Commit-2 "fire on every caller" behaviour. The Noop pattern
// abandons those observations in tests + partial-deploy paths
// intentionally: tests don't need to assert metric increments,
// and partial-deploy paths shouldn't pollute the collected metrics
// with placeholder data.
type NoopMetricsRecorder struct{}

// Compile-time assertion: NoopMetricsRecorder satisfies MetricsRecorder.
var _ MetricsRecorder = (*NoopMetricsRecorder)(nil)

// IncVideosChecked is a no-op.
func (NoopMetricsRecorder) IncVideosChecked(string) {}

// IncVideosWithSegments is a no-op.
func (NoopMetricsRecorder) IncVideosWithSegments(string) {}

// AddSegmentsFound is a no-op.
func (NoopMetricsRecorder) AddSegmentsFound(string, int) {}

// ObserveSegmentsPerVideo is a no-op.
func (NoopMetricsRecorder) ObserveSegmentsPerVideo(string, int) {}
