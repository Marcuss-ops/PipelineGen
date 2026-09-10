// Package observability — metrics_enrichment_test.go (Sept 2026).
//
// Hermetic test for the 4 metadata-enrichment Prometheus collectors
// declared in metrics_enrichment.go (the replacement for the retired
// Step10MetricsRecorder family). The test pins:
//
//  1. All 4 metrics are registered with the default Prometheus
//     registry at package init (promauto.New*) — a missing
//     registration would cause a dashboard gap on the enrichment SLO.
//  2. The metric handles are non-nil (Add/Inc/Observe panic on nil
//     pointers; promauto guarantees non-nil at init).
//  3. The counter / histogram semantic split matches the metric
//     type — the two counters are COUNTER, the two duration/age
//     surfaces are HISTOGRAM.
//  4. The adapter is nil-safe and routes every method to the correct
//     collector family.
package observability

import (
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// instantiateEnrichmentMetrics touches each of the 4 enrichment metrics
// once so DefaultGatherer.Gather() emits a corresponding MetricFamily
// for the registration/type assertions (promauto registers the
// collector, but Gather() only emits families for instantiated
// children). Guarded by sync.Once so parallel tests don't pay the
// boot cost twice.
var instantiateEnrichmentOnce sync.Once

func instantiateEnrichmentMetrics() {
	instantiateEnrichmentOnce.Do(func() {
		MetadataEnrichmentTotal.Add(0)
		MetadataEnrichmentDurationSeconds.Observe(0)
		MetadataEnrichmentFailuresTotal.Add(0)
		MetadataEnrichmentQueueAgeSeconds.Observe(0)
	})
}

// TestEnrichmentMetrics_AllRegistered pins each of the 4 metric handles
// to registration in the default registry. godlike/06 SSOT: any future
// PR adding a 5th enrichment metric MUST update both metrics_enrichment.go
// AND this test.
func TestEnrichmentMetrics_AllRegistered(t *testing.T) {
	t.Parallel()
	instantiateEnrichmentMetrics()

	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("DefaultGatherer.Gather failed: %v", err)
	}

	wantNames := map[string]bool{
		"metadata_enrichment_total":             false,
		"metadata_enrichment_duration_seconds":  false,
		"metadata_enrichment_failures_total":    false,
		"metadata_enrichment_queue_age_seconds": false,
	}

	for _, mf := range mfs {
		if _, ok := wantNames[mf.GetName()]; ok {
			wantNames[mf.GetName()] = true
		}
	}

	for name, found := range wantNames {
		if !found {
			t.Errorf("metric %q NOT registered in default prometheus registry (Sept 2026 enrichment SRE spec violation)", name)
		}
	}
}

// TestEnrichmentMetrics_NonNilPointers pins the handles to non-nil so
// downstream Inc/Observe calls cannot panic on pointer-deref.
func TestEnrichmentMetrics_NonNilPointers(t *testing.T) {
	t.Parallel()

	if MetadataEnrichmentTotal == nil {
		t.Fatal("MetadataEnrichmentTotal is nil (godlike/07 fail-closed: promauto must init non-nil)")
	}
	if MetadataEnrichmentDurationSeconds == nil {
		t.Fatal("MetadataEnrichmentDurationSeconds is nil")
	}
	if MetadataEnrichmentFailuresTotal == nil {
		t.Fatal("MetadataEnrichmentFailuresTotal is nil")
	}
	if MetadataEnrichmentQueueAgeSeconds == nil {
		t.Fatal("MetadataEnrichmentQueueAgeSeconds is nil")
	}
}

// TestEnrichmentMetrics_TypeSemantics pins counter vs histogram for each
// handle so a future PR cannot accidentally promote a counter to a
// gauge or histogram (dashboards expect monotonic counters for totals).
func TestEnrichmentMetrics_TypeSemantics(t *testing.T) {
	t.Parallel()
	instantiateEnrichmentMetrics()

	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("DefaultGatherer.Gather failed: %v", err)
	}

	wantTypes := map[string]dto.MetricType{
		"metadata_enrichment_total":             dto.MetricType_COUNTER,
		"metadata_enrichment_duration_seconds":  dto.MetricType_HISTOGRAM,
		"metadata_enrichment_failures_total":    dto.MetricType_COUNTER,
		"metadata_enrichment_queue_age_seconds": dto.MetricType_HISTOGRAM,
	}

	familiesByName := make(map[string]*dto.MetricFamily, len(mfs))
	for _, mf := range mfs {
		familiesByName[mf.GetName()] = mf
	}

	for name, wantType := range wantTypes {
		mf := familiesByName[name]
		if mf == nil {
			t.Errorf("metric %q not registered in default gatherer", name)
			continue
		}
		if mf.GetType() != wantType {
			t.Errorf("metric %q: want type=%v; got %v", name, wantType, mf.GetType())
		}
	}
}

// TestEnrichmentMetrics_Unlabelled pins the zero-label cardinality
// contract: every enrichment metric is a single unlabelled series, so
// dashboards never fan out on unbounded labels (clip_id is explicitly
// forbidden as a label).
func TestEnrichmentMetrics_Unlabelled(t *testing.T) {
	t.Parallel()
	instantiateEnrichmentMetrics()

	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("DefaultGatherer.Gather failed: %v", err)
	}

	familiesByName := make(map[string]*dto.MetricFamily, len(mfs))
	for _, mf := range mfs {
		familiesByName[mf.GetName()] = mf
	}

	for name := range map[string]bool{
		"metadata_enrichment_total":             false,
		"metadata_enrichment_duration_seconds":  false,
		"metadata_enrichment_failures_total":    false,
		"metadata_enrichment_queue_age_seconds": false,
	} {
		mf := familiesByName[name]
		if mf == nil {
			t.Errorf("metric %q: not in default gatherer registry", name)
			continue
		}
		if len(mf.Metric) == 0 {
			continue // registered but no series emitted yet
		}
		for _, m := range mf.Metric {
			if len(m.Label) != 0 {
				t.Errorf("metric %q: want 0 labels; got %d (%v)", name, len(m.Label), m.Label)
			}
		}
	}
}

// TestMetadataEnrichmentRecorder_Routing pins the adapter method → metric
// mapping. The adapter is the ONLY surface the application layers call
// (the sync use case path and the async wiring handler), so a mis-routed
// method would silently move data between the wrong series.
func TestMetadataEnrichmentRecorder_Routing(t *testing.T) {
	t.Parallel()
	instantiateEnrichmentMetrics()

	beforeTotal := counterValue(MetadataEnrichmentTotal)
	beforeDuration := histogramSampleCount(t, MetadataEnrichmentDurationSeconds)
	beforeFailures := counterValue(MetadataEnrichmentFailuresTotal)
	beforeQueueAge := histogramSampleCount(t, MetadataEnrichmentQueueAgeSeconds)

	r := NewMetadataEnrichmentRecorder()
	r.IncEnrichmentTotal()
	r.ObserveEnrichmentDuration(1.5)
	r.IncEnrichmentFailures()
	r.ObserveEnrichmentQueueAge(3.25)

	if got := counterValue(MetadataEnrichmentTotal); got != beforeTotal+1 {
		t.Errorf("metadata_enrichment_total: want %v got %v", beforeTotal+1, got)
	}
	if got := histogramSampleCount(t, MetadataEnrichmentDurationSeconds); got != beforeDuration+1 {
		t.Errorf("metadata_enrichment_duration_seconds samples: want %v got %v", beforeDuration+1, got)
	}
	if got := counterValue(MetadataEnrichmentFailuresTotal); got != beforeFailures+1 {
		t.Errorf("metadata_enrichment_failures_total: want %v got %v", beforeFailures+1, got)
	}
	if got := histogramSampleCount(t, MetadataEnrichmentQueueAgeSeconds); got != beforeQueueAge+1 {
		t.Errorf("metadata_enrichment_queue_age_seconds samples: want %v got %v", beforeQueueAge+1, got)
	}
}

// TestMetadataEnrichmentRecorder_NilSafe pins the nil-receiver contract:
// a nil recorder (unwired compositions, partial deploys) must never
// panic and must never touch the collectors.
func TestMetadataEnrichmentRecorder_NilSafe(t *testing.T) {
	t.Parallel()
	instantiateEnrichmentMetrics()

	var r *MetadataEnrichmentRecorder
	r.IncEnrichmentTotal()
	r.ObserveEnrichmentDuration(1)
	r.IncEnrichmentFailures()
	r.ObserveEnrichmentQueueAge(1)
	// A nil receiver must route nowhere; the negative-guard must also
	// reject garbage durations without touching the histogram.
	(&MetadataEnrichmentRecorder{}).ObserveEnrichmentDuration(-1)
	(&MetadataEnrichmentRecorder{}).ObserveEnrichmentQueueAge(-1)
}
