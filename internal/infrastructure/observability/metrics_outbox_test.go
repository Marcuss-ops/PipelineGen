// Package observability — metrics_outbox_test.go (Fase 6(c) Push 6.2, July 2026).
//
// Hermetic test for the 5 outbox Prometheus collectors declared in
// metrics_outbox.go. The test pins:
//
//  1. All 5 metrics are registered with the default Prometheus
//     registry at package init (promauto.New*Vec) — a missing
//     registration would cause a downstream-metric panic or a
//     dashboard gap.
//
//  2. The metric handles are non-nil (Add/Set/Inc/WithLabelValues
//     panic on nil pointers; promauto guarantees non-nil at init).
//
//  3. The counter / histogram / gauge semantic split matches the
//     metric type — OutboxLagSeconds is a GaugeVec
//     (Set per event), OutboxDispatchDurationSeconds is a
//     HistogramVec (Observe per dispatch), the 3 counters are
//     Counter/CounterVec.
//
// The test does NOT verify specific labels or values — that is
// the integration test's surface (storage_pool_test.go / pool observational
// tests). This unit test cares that the COLLECTORS exist and are
// registered so /metrics cannot return a 500 in production due to a
// missing series.
package observability

import (
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// instantiateOutboxMetrics touches each of the 5 Push 6.2 outbox
// metrics once with a placeholder label set so DefaultGatherer.Gather()
// emits a corresponding MetricFamily for the test assertions.
//
// Why this is necessary: promauto.New*Vec registers the collector
// against the default registry at package init, but the Gather()
// round-trip only emits MetricFamily entries for *instantiated*
// Children (those that have been touched via WithLabelValues + an
// Inc/Set/Observe call). Without this bootstrapping, tests that call
// Gather() in isolation (e.g. parallel test runs) see an empty
// MetricFamily set for the Vec-typed metrics and fail.
//
// Guarded by sync.Once so concurrent parallel tests don't pay the
// (cheap) boot cost twice.
var instantiateOnce sync.Once

func instantiateOutboxMetrics() {
	instantiateOnce.Do(func() {
		OutboxLagSeconds.WithLabelValues("test:boot").Set(0)
		OutboxDispatchDurationSeconds.WithLabelValues("test:boot", "ok").Observe(0)
		OutboxReclaimTotal.Add(0)
		OutboxDLQTotal.WithLabelValues("test:boot").Add(0)
		OutboxRetriesTotal.WithLabelValues("test:boot").Add(0)
	})
}

// TestOutboxMetrics_AllRegistered pins each of the 5 metric handles
// to a non-nil pointer + registration in the default registry. The
// canonical collection-set pin is godlike/06 SSOT: any future PR
// adding a 6th metric MUST update both the metrics_outbox.go surface
// AND this test, with the round-trip check built into CI.
//
// Failure surface (godlike/07 NO-FAKE-AVAILABILITY): if a metric
// declaration is missing from metrics_outbox.go (e.g. accidentally
// commented out), `init()` does not register the collector → the
// /metrics endpoint returns HTTP 200 with the series absent.
// Operators reading the dashboard then see "no data" which the user
// spec calls out as fake availability. This test fails-fast at CI.
func TestOutboxMetrics_AllRegistered(t *testing.T) {
	t.Parallel()
	instantiateOutboxMetrics()

	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("DefaultGatherer.Gather failed: %v", err)
	}

	wantNames := map[string]bool{
		"outbox_lag_seconds":               false,
		"outbox_dispatch_duration_seconds": false,
		"outbox_reclaim_total":             false,
		"outbox_dlq_total":                 false,
		"outbox_retries_total":             false,
	}

	for _, mf := range mfs {
		if _, ok := wantNames[mf.GetName()]; ok {
			wantNames[mf.GetName()] = true
		}
	}

	for name, found := range wantNames {
		if !found {
			t.Errorf("metric %q NOT registered in default prometheus registry (Push 6.2 spec violation)", name)
		}
	}
}

// TestOutboxMetrics_NonNilPointers pins the handles to non-nil so
// downstream Add/Set/Inc/WithLabelValues calls cannot panic on
// pointer-deref. promauto guarantees non-nil at package init, but
// a human migration (e.g. accidentally typed `var X = nil` in the
// metrics_outbox.go file) would silently produce a nil handle and
// then panic at metric call time. This test pins that invariant.
func TestOutboxMetrics_NonNilPointers(t *testing.T) {
	t.Parallel()

	if OutboxLagSeconds == nil {
		t.Fatal("OutboxLagSeconds is nil (godlike/07 fail-closed: promauto must init non-nil)")
	}
	if OutboxDispatchDurationSeconds == nil {
		t.Fatal("OutboxDispatchDurationSeconds is nil")
	}
	if OutboxReclaimTotal == nil {
		t.Fatal("OutboxReclaimTotal is nil")
	}
	if OutboxDLQTotal == nil {
		t.Fatal("OutboxDLQTotal is nil")
	}
	if OutboxRetriesTotal == nil {
		t.Fatal("OutboxRetriesTotal is nil")
	}
}

// TestOutboxMetrics_TypeSemantics pins the metric type semantic for
// each handle (gauge vs counter vs histogram) so a future PR that
// changes the type cannot accidentally promote a counter to a gauge
// (gauge-loses-monotonicity on restart; counter is what dashboards
// expect).
//
// Implementation note: the canonical Prometheus type-pinning pattern
// uses the dto.MetricFamily gathered from DefaultGatherer (which
// reflects the registered collector type) rather than the
// *prometheus.GaugeVec / *prometheus.Counter / *prometheus.CounterVec
// handles directly — those types do NOT expose a `.GetType()`
// accessor (only the embedded `MetricVec` exposes the dto type via
// the gatherer round-trip). This keeps the test in lockstep with
// the `prometheus.DefaultGatherer.Gather()` pattern used by
// TestOutboxMetrics_AllRegistered.
func TestOutboxMetrics_TypeSemantics(t *testing.T) {
	t.Parallel()
	instantiateOutboxMetrics()

	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("DefaultGatherer.Gather failed: %v", err)
	}

	wantTypes := map[string]dto.MetricType{
		"outbox_lag_seconds":               dto.MetricType_GAUGE,
		"outbox_dispatch_duration_seconds": dto.MetricType_HISTOGRAM,
		"outbox_reclaim_total":             dto.MetricType_COUNTER,
		"outbox_dlq_total":                 dto.MetricType_COUNTER,
		"outbox_retries_total":             dto.MetricType_COUNTER,
	}

	familiesByName := make(map[string]*dto.MetricFamily, len(mfs))
	for _, mf := range mfs {
		familiesByName[mf.GetName()] = mf
	}

	for name, wantType := range wantTypes {
		mf := familiesByName[name]
		if mf == nil {
			t.Errorf("metric %q not registered in default gatherer (Push 6.2 spec violation)", name)
			continue
		}
		if mf.GetType() != wantType {
			t.Errorf("metric %q: want type=%v; got %v", name, wantType, mf.GetType())
		}
	}
}

// TestOutboxMetrics_LabelShapes pins the canonical label cardinality
// for each metric. Drift here breaks dash cardinality projections
// written against the documented labels.
func TestOutboxMetrics_LabelShapes(t *testing.T) {
	t.Parallel()
	instantiateOutboxMetrics()

	// Label-shape pin via the gathererround-trip. The
	// *prometheus.GaugeVec / *CounterVec handle does not expose the
	// label-descriptor via a public Desc() accessor (only the
	// embedded MetricVec carries it as an unexported field), so the
	// canonical pattern is to walk the gathered MetricFamily —
	// which exposes both label names and observed values.

	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("DefaultGatherer.Gather failed: %v", err)
	}

	wantLabels := map[string][]string{
		"outbox_lag_seconds":               {"event_type"},
		"outbox_dispatch_duration_seconds": {"event_type", "outcome"},
		"outbox_reclaim_total":             {},
		"outbox_dlq_total":                 {"event_type"},
		"outbox_retries_total":             {"event_type"},
	}

	familiesByName := make(map[string]*dto.MetricFamily, len(mfs))
	for _, mf := range mfs {
		familiesByName[mf.GetName()] = mf
	}

	for name, expected := range wantLabels {
		mf := familiesByName[name]
		if mf == nil {
			t.Errorf("metric %q: not in default gatherer registry", name)
			continue
		}
		// Extract label names from the metric's desc (which carries
		// the label-shape declaration). The gatherer exposes the
		// label names via the first metric's Label entries.
		if len(mf.Metric) == 0 {
			continue // registered but no series emitted yet
		}
		var gotLabels []string
		for _, lp := range mf.Metric[0].Label {
			gotLabels = append(gotLabels, lp.GetName())
		}
		if len(gotLabels) != len(expected) {
			t.Errorf("metric %q: want %d labels %v; got %d %v", name, len(expected), expected, len(gotLabels), gotLabels)
		}
	}
}
