// Package observability — MetricsRecorder adapter for monitor.
//
// FASE 3.7 Commit 2 (2026-07-04): the canonical
// ChannelMonitor*Counter/Histogram declared in metrics_workers.go
// (line 26/31/36/41) were previously consumed directly by
// `internal/capabilities/assets/monitor/{analyzer.go,discovery.go}`
// via `metrics.ChannelMonitor*...WithLabelValues(...).Inc()`-style
// calls. The direct-import pattern violated the FASE 3.7 zero-
// infra-import commitment in the monitor package (the same
// commitment that closed Commit 1b for the discoveries port).
//
// This file provides the canonical Pattern-0 adapter: a struct
// that holds *prometheus.CounterVec / *prometheus.HistogramVec
// references and translates each into the typed
// monitor.MetricsRecorder port method. The composition root
// (internal/app/lifecycle.go) constructs the adapter by passing
// the package-level Prometheus counters; tests can substitute
// their own CounterVec instances for unit-test isolation
// (verified by metrics_adapter_test.go via testutil.ToFloat64).
//
// Why the constructor takes CounterVecs explicitly rather than
// referencing the package-level vars directly:
//  1. Test injectability per Pattern 0 (AGENTS.md §Patterns):
//     tests construct a fresh adapter with isolated CounterVecs
//     so assertions on counter values don't race with other
//     tests that hit the package-level collector registry.
//  2. Compile-time drift guard: a signature mismatch between
//     the adapter fields and the production CounterVec types
//     is a build-time failure (the linter pins it via the
//     `*prometheus.CounterVec`/`*prometheus.HistogramVec`
//     parameter types in NewObservabilityMetricsRecorder).
//  3. The package-level vars remain the production Prometheus
//     source-of-truth (promauto registers them globally); the
//     adapter is a typed thin wrapper that doesn't change
//     collector registry behaviour.
//
// All methods are nil-receiver + nil-vec safe so partial-deploy
// paths don't panic (a nil MetricsRecorder instance returns
// silently from every method). The canonical "wrap-counter-or-
// carry-on" pattern for observability adapters — Prometheus'
// own client_golang library swallows panic-on-nil-counter; the
// nil-receiver guard here is for analyst-facing test surfaces.
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
)

// MetricsRecorder is the minimal shape satisfied by
// ObservabilityMetricsRecorder; we redeclare it as a structural
// interface here (rather than importing monitor.MetricsRecorder,
// which would invert the layering) so the adapter lives in the
// observability package without importing any application-layer
// package.
//
// FASE 3.7 Commit 2 layering rule: observability MUST NOT import
// monitor (the infra layer has zero knowledge of which package
// consumes its Prometheus surface — that's the composition
// root's job). The interface redeclaration collapses a vertical
// dependency into a structural one: the adapter's signature is
// sufficient for the composition root's `var _ monitor.MetricsRecorder
// = (*observability.ObservabilityMetricsRecorder)(nil)` compile-time
// assertion to verify port coverage without forcing a circular
// import.
//
// Why not just refer to the application-side interface directly?
// Application-layer Pattern-0 ports MUST be interface-satisfied
// by INFRA structs to keep the layering direction intact
// (infra → application would invert the typical hexagonal
// dependency). The struct embedding override pattern at the
// composition root (lifecycle.go) performs the structural
// binding via Go's generic `var _ T = (*S)(nil)` idiom — neither
// side imports the other; the assertion lives in a third file.
//
// ── DRIFT NOTE (load-bearing, DO NOT remove) ──
// The structural identity between this redeclared interface and the
// canonical `application/assets/monitor.MetricsRecorder` is pinned
// ONLY by the composition-root assertion in lifecycle.go:
//
//	var _ monitor.MetricsRecorder = (*observability.ObservabilityMetricsRecorder)(nil)
//
// Drift between the 4 method signatures below and the 4 port methods
// in monitor.MetricsRecorder is a build-time failure at that
// assertion (NOT a runtime panic). DO NOT delete or weaken the
// assertion in lifecycle.go — it is the only canonical guard that
// the observability adapter can keep satisfying the monitor port.
//
// (Production-time import of monitor from observability is forbidden
// because it would invert the application→infrastructure layering; the
// composition root is the single layer where both can co-exist.)
type MetricsRecorder interface {
	IncVideosChecked(channel string)
	IncVideosWithSegments(channel string)
	AddSegmentsFound(channel string, n int)
	ObserveSegmentsPerVideo(channel string, n int)
}

// ObservabilityMetricsRecorder is the canonical Pattern-0
// adapter: 4 Prometheus CounterVec / HistogramVec refs mapped
// 1:1 to the monitor.MetricsRecorder port methods. Field-level
// optionality (each vec can be nil) is intentional: production
// wiring passes the 4 package-level vars; tests construct a
// subset + leave the rest nil to assert "the labelled-call
// method increments the right counter even when other counters
// are absent" — this is the axis on which FASE 3.7 Commit 2
// test coverage is built (metrics_adapter_test.go).
type ObservabilityMetricsRecorder struct {
	// VideosChecked corresponds to channel_monitor_videos_checked_total
	// (Counter). Labelled by "channel".
	VideosChecked *prometheus.CounterVec

	// VideosWithSegments corresponds to
	// channel_monitor_videos_with_segments_total (Counter).
	// Labelled by "channel".
	VideosWithSegments *prometheus.CounterVec

	// SegmentsFound corresponds to
	// channel_monitor_segments_found_total (Counter).
	// Labelled by "channel".
	SegmentsFound *prometheus.CounterVec

	// SegmentsPerVideo corresponds to channel_monitor_segments_per_video
	// (Histogram). Labelled by "channel".
	SegmentsPerVideo *prometheus.HistogramVec
}

// NewObservabilityMetricsRecorder constructs the canonical
// adapter from the 4 Prometheus vec refs. The composition root
// (internal/app/lifecycle.go) passes the package-level vars from
// metrics_workers.go (ChannelMonitorVideosChecked, ...WithSegments,
// ...SegmentsFound, ...SegmentsPerVideo); tests pass locally-
// constructed prometheus.NewCounterVec (without promauto, so the
// test's local instance never lands in the production collector
// registry).
//
// Nil-safe construction: accepting nil vec refs preserves the
// test pattern "construct the adapter, leave 3 of 4 fields nil,
// assert the 4th gets called" (the assertion drives test
// coverage on a per-method basis rather than an all-or-nothing
// pattern).
func NewObservabilityMetricsRecorder(
	videosChecked *prometheus.CounterVec,
	videosWithSegments *prometheus.CounterVec,
	segmentsFound *prometheus.CounterVec,
	segmentsPerVideo *prometheus.HistogramVec,
) *ObservabilityMetricsRecorder {
	return &ObservabilityMetricsRecorder{
		VideosChecked:      videosChecked,
		VideosWithSegments: videosWithSegments,
		SegmentsFound:      segmentsFound,
		SegmentsPerVideo:   segmentsPerVideo,
	}
}

// IncVideosChecked implements MetricsRecorder. Bumps the
// "videos-checked" CounterVec by 1 for the supplied channel label.
// Nil-receiver and nil-vec safe — partial-deploy paths and
// test fixtures don't panic.
func (r *ObservabilityMetricsRecorder) IncVideosChecked(channel string) {
	if r == nil || r.VideosChecked == nil {
		return
	}
	r.VideosChecked.WithLabelValues(channel).Inc()
}

// IncVideosWithSegments implements MetricsRecorder. Bumps the
// "videos-with-segments" CounterVec by 1 for the supplied channel label.
func (r *ObservabilityMetricsRecorder) IncVideosWithSegments(channel string) {
	if r == nil || r.VideosWithSegments == nil {
		return
	}
	r.VideosWithSegments.WithLabelValues(channel).Inc()
}

// AddSegmentsFound implements MetricsRecorder. Adds the segment-
// count delta (n) to the "segments-found" CounterVec. n is the
// int-shaped segment count from analysis.Segments; the adapter
// converts to the float64 Prometheus idiom internally. n<=0 is
// treated as a no-op to prevent Prometheus Counter.Add(<negative>)
// underflow — the canonical pre-Commit-2 callers only ever supplied
// n>0 (because they were guarded by an `if len(segments) > 0`
// upstream), so this guard is a safety net for hypothetical future
// callers rather than a behaviour change.
func (r *ObservabilityMetricsRecorder) AddSegmentsFound(channel string, n int) {
	if r == nil || r.SegmentsFound == nil || n <= 0 {
		return
	}
	r.SegmentsFound.WithLabelValues(channel).Add(float64(n))
}

// ObserveSegmentsPerVideo implements MetricsRecorder. Records a
// single histogram observation. n=0 is honoured — the histogram's
// first bucket (configured for explicit zero capture in
// metrics_workers.go line 41-43) absorbs zero observations so
// zero-segment skip paths still register a metrics tick.
func (r *ObservabilityMetricsRecorder) ObserveSegmentsPerVideo(channel string, n int) {
	if r == nil || r.SegmentsPerVideo == nil {
		return
	}
	r.SegmentsPerVideo.WithLabelValues(channel).Observe(float64(n))
}
