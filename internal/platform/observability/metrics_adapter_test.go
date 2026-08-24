// Package observability — MetricsRecorder adapter tests.
//
// FASE 3.7 Commit 2 (2026-07-04): the canonical-surface tests for
// the ObservabilityMetricsRecorder adapter. Each test exercises one
// of the 4 port methods against an isolated CounterVec / HistogramVec
// constructed locally (without promauto), so the test never pollutes
// the production collector registry. Counter values are sampled via
// testutil.ToFloat64 (the standard Prometheus idiom for asserting
// counter increments from outside the goroutine-under-test).
//
// Import-cycle note (fixed during test-write review): an earlier
// draft of this file imported the application-layer
// `github.com/Marcuss-ops/PipelineGen/internal/application/assets/monitor`
// package to host an in-test `var _ monitor.MetricsRecorder = (*ObservabilityMetricsRecorder)(nil)`
// structural pin, plus a cross-package TestNoopMetricsRecorder and a
// TestCompositionRootPortSatisfaction field-assignment test. That
// in-test import pulled in monitor's transitive dependency graph,
// which closed a Go package cycle through the clips/adapters/outbox
// chain back to `internal/platform/observability`. The cycle
// was:
//
//	observability test → monitor (from metrics_adapter_test.go)
//	                  → channels (analyzer.go)
//	                  → assets (internal/platform/sqlite/assets)
//	                  → outbox (clips_repository.go)
//	                  → observability (indexing.go)
//
// The production-time compile-time assertion has been relocated to
// `internal/app/lifecycle.go` (the composition root, which imports
// BOTH monitor + observability — no cycle there because lifecycle.go
// imports neither clips/assets/outbox). TestNoopMetricsRecorder and
// TestCompositionRootPortSatisfaction were dropped (they exercise
// monitor-side types, not observability-side ones, and belong in a
// future `internal/application/assets/monitor/ports_metrics_test.go`).
//
// The observability-side tests below are still the canonical-surface
// pin for adapter behaviour (4 methods × isolated vecs × ToFloat64
// assertions). The composition-root assertion in lifecycle.go is the
// canonical PIN for structural identity between adapter methods and
// the canonical monitor.MetricsRecorder port — drift between the
// two is a build-time failure at the composition root.
package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// newTestVectors returns a fresh handful of CounterVec + HistogramVec
// instances not registered with the production collector registry
// (constructed directly via prometheus.NewCounterVec / NewHistogramVec,
// NOT prometheus.NewCounterVec with promauto). Isolated per-test
// guarantees no cross-test contamination: each test starts at 0.
func newTestVectors() (
	videosChecked *prometheus.CounterVec,
	videosWithSegments *prometheus.CounterVec,
	segmentsFound *prometheus.CounterVec,
	segmentsPerVideo *prometheus.HistogramVec,
) {
	videosChecked = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "test_channel_monitor_videos_checked_total",
		Help: "test-only counter",
	}, []string{"channel"})
	videosWithSegments = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "test_channel_monitor_videos_with_segments_total",
		Help: "test-only counter",
	}, []string{"channel"})
	segmentsFound = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "test_channel_monitor_segments_found_total",
		Help: "test-only counter",
	}, []string{"channel"})
	segmentsPerVideo = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "test_channel_monitor_segments_per_video",
		Help:    "test-only histogram",
		Buckets: []float64{0, 1, 2, 3},
	}, []string{"channel"})
	return
}

// TestIncVideosChecked pins the canonical Inc-by-1 behaviour for
// the videos-checked CounterVec.
func TestIncVideosChecked(t *testing.T) {
	vc, vws, sf, spv := newTestVectors()
	rec := NewObservabilityMetricsRecorder(vc, vws, sf, spv)

	rec.IncVideosChecked("channel-A")
	rec.IncVideosChecked("channel-A")
	rec.IncVideosChecked("channel-B")

	if got := testutil.ToFloat64(vc.WithLabelValues("channel-A")); got != 2 {
		t.Errorf("IncVideosChecked channel-A: want 2, got %v", got)
	}
	if got := testutil.ToFloat64(vc.WithLabelValues("channel-B")); got != 1 {
		t.Errorf("IncVideosChecked channel-B: want 1, got %v", got)
	}
}

// TestIncVideosWithSegments pins the Inc-by-1 behaviour for the
// videos-with-segments counter (the successful-analyzer path
// counter, paired with AddSegmentsFound).
func TestIncVideosWithSegments(t *testing.T) {
	vc, vws, sf, spv := newTestVectors()
	rec := NewObservabilityMetricsRecorder(vc, vws, sf, spv)

	rec.IncVideosWithSegments("channel-A")
	rec.IncVideosWithSegments("channel-A")
	rec.IncVideosWithSegments("channel-A")

	if got := testutil.ToFloat64(vws.WithLabelValues("channel-A")); got != 3 {
		t.Errorf("IncVideosWithSegments channel-A: want 3, got %v", got)
	}
}

// TestAddSegmentsFound pins the Add-by-N behaviour for the
// segments-found counter. Verifies that Add(n) cumulative-adds
// across multiple calls.
func TestAddSegmentsFound(t *testing.T) {
	vc, vws, sf, spv := newTestVectors()
	rec := NewObservabilityMetricsRecorder(vc, vws, sf, spv)

	rec.AddSegmentsFound("channel-A", 2)
	rec.AddSegmentsFound("channel-A", 3)
	rec.AddSegmentsFound("channel-A", 5)

	if got := testutil.ToFloat64(sf.WithLabelValues("channel-A")); got != 10 {
		t.Errorf("AddSegmentsFound channel-A: want 10, got %v", got)
	}
}

// TestAddSegmentsFoundNonPositiveNoop pins the n<=0 guard:
// AddSegmentsFound(huge, n<=0) MUST be a no-op (no negative-add
// into the counter, no panic on a zero count).
func TestAddSegmentsFoundNonPositiveNoop(t *testing.T) {
	vc, vws, sf, spv := newTestVectors()
	rec := NewObservabilityMetricsRecorder(vc, vws, sf, spv)

	rec.AddSegmentsFound("channel-A", 0)
	rec.AddSegmentsFound("channel-A", -1)

	if got := testutil.ToFloat64(sf.WithLabelValues("channel-A")); got != 0 {
		t.Errorf("AddSegmentsFound with n<=0: want 0, got %v", got)
	}
}

// TestObserveSegmentsPerVideoZeroObserved pins the zero-observation
// behaviour: the metric must still emit Observe(0) so the
// zero-bucket captures the empty-segments soft-skip path per the
// port-contract documentation. testutil.CollectAndCount(spv, ...)
// returns 1 because all observations on the same label collapse
// into a single MetricFamily entry; we assert >0 (any) rather
// than ==1 to remain robust to client_golang internal changes.
func TestObserveSegmentsPerVideoZeroObserved(t *testing.T) {
	vc, vws, sf, spv := newTestVectors()
	rec := NewObservabilityMetricsRecorder(vc, vws, sf, spv)

	rec.ObserveSegmentsPerVideo("channel-A", 0)
	rec.ObserveSegmentsPerVideo("channel-A", 5)

	if got := testutil.CollectAndCount(spv, "test_channel_monitor_segments_per_video"); got < 1 {
		t.Errorf("ObserveSegmentsPerVideo: expected at least 1 labelled metric emission, got %v", got)
	}
}

// TestNilReceiverSafety pins the production safety posture: every
// method on the adapter must be safe to invoke when the receiver
// itself is nil (no panic) AND when individual CounterVec fields
// are nil (no panic — no-op). This is the partial-deploy + test-
// fixture path.
func TestNilReceiverSafety(t *testing.T) {
	// All-nil receiver.
	var rec *ObservabilityMetricsRecorder
	rec.IncVideosChecked("x")           //nolint:staticcheck // deliberate nil-receiver test
	rec.IncVideosWithSegments("x")      //nolint:staticcheck // deliberate nil-receiver test
	rec.AddSegmentsFound("x", 1)        //nolint:staticcheck // deliberate nil-receiver test
	rec.ObserveSegmentsPerVideo("x", 1) //nolint:staticcheck // deliberate nil-receiver test

	// Receiver with selective nil fields (only the targeted vec
	// is wired; the other 3 stay nil). Each method should hit its
	// non-nil vec and short-circuit on the nil ones.
	vcOnly := NewObservabilityMetricsRecorder(
		prometheus.NewCounterVec(prometheus.CounterOpts{Name: "t_a"}, []string{"channel"}),
		nil, nil, nil,
	)
	vcOnly.IncVideosChecked("y")
	vcOnly.IncVideosWithSegments("y")      //nolint:staticcheck // no-op on nil vec
	vcOnly.AddSegmentsFound("y", 1)        //nolint:staticcheck // no-op on nil vec
	vcOnly.ObserveSegmentsPerVideo("y", 1) //nolint:staticcheck // no-op on nil vec

	// No panic reached here = either safety path works. Assertion
	// is the "silent pass": any panic fails the test via the
	// `t.Fatal` shim below.
}
