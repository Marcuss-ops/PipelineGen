// Package observability — RunReportsCollector adapter tests (FASE 2,
// August 2026).
//
// Each test injects hermetic isolated vectors (never the production
// default-registry globals), mirroring the metrics_adapter_test.go
// pattern. Histogram sums are asserted via the Histogram interface
// (SampleCount / Sum).
package observability

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

func newTestRunReportVectors() (duration *prometheus.HistogramVec, queueWait *prometheus.HistogramVec, retries *prometheus.CounterVec) {
	duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "test_job_run_duration_seconds",
		Help:    "test-only histogram",
		Buckets: []float64{1, 5, 10},
	}, []string{"job_type", "status"})
	queueWait = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "test_job_run_queue_wait_seconds",
		Help:    "test-only histogram",
		Buckets: []float64{1, 5, 10},
	}, []string{"job_type"})
	retries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "test_job_run_retries_total",
		Help: "test-only counter",
	}, []string{"job_type"})
	return
}

// histogramDTO decodes an observer's underlying metric into the DTO
// histogram so tests can assert SampleCount / SampleSum (the exported
// prometheus.Histogram interface only exposes Observe).
func histogramDTO(t *testing.T, obs prometheus.Observer) *dto.Histogram {
	t.Helper()
	m := &dto.Metric{}
	metric, ok := obs.(prometheus.Metric)
	if !ok {
		t.Fatalf("observer is not a prometheus.Metric: %T", obs)
	}
	if err := metric.Write(m); err != nil {
		t.Fatalf("metric.Write: %v", err)
	}
	return m.GetHistogram()
}

// TestRunReportsCollector_ObservesWallQueueWaitAndRetries pins the
// core translation: WallTimeMs → duration histogram (seconds, by
// job_type+status), QueueWaitMs → queue-wait histogram (seconds, by
// job_type), Counters.Retries → retries counter (by job_type).
func TestRunReportsCollector_ObservesWallQueueWaitAndRetries(t *testing.T) {
	duration, queueWait, retries := newTestRunReportVectors()
	c := newRunReportsCollectorWithVectors(duration, queueWait, retries)

	if err := c.Collect(context.Background(), &kernobs.RunReport{
		JobID: "job-1", JobType: "script.generate", Status: kernobs.StatusSucceeded,
		WallTimeMs: 5000, QueueWaitMs: 450,
		Counters: kernobs.RunCounters{Retries: 2},
	}); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	d, err := duration.GetMetricWithLabelValues("script.generate", "SUCCEEDED")
	if err != nil {
		t.Fatalf("duration metric: %v", err)
	}
	dh := histogramDTO(t, d)
	if dh.GetSampleCount() != 1 {
		t.Errorf("duration sample count = %d, want 1", dh.GetSampleCount())
	}
	if dh.GetSampleSum() != 5.0 {
		t.Errorf("duration sum = %v, want 5.0 (5000ms / 1000)", dh.GetSampleSum())
	}

	q, err := queueWait.GetMetricWithLabelValues("script.generate")
	if err != nil {
		t.Fatalf("queue-wait metric: %v", err)
	}
	qh := histogramDTO(t, q)
	if qh.GetSampleSum() != 0.45 {
		t.Errorf("queue-wait sum = %v, want 0.45 (450ms / 1000)", qh.GetSampleSum())
	}

	if got := testutil.ToFloat64(retries.WithLabelValues("script.generate")); got != 2 {
		t.Errorf("retries = %v, want 2", got)
	}
}

// TestRunReportsCollector_SkipsZeroValues pins the zero-guards:
// WallTimeMs / QueueWaitMs / Retries of zero must not create labelled
// time-series (no metric pollution from idle runs).
func TestRunReportsCollector_SkipsZeroValues(t *testing.T) {
	duration, queueWait, retries := newTestRunReportVectors()
	c := newRunReportsCollectorWithVectors(duration, queueWait, retries)

	if err := c.Collect(context.Background(), &kernobs.RunReport{
		JobID: "job-2", JobType: "stock.run", Status: kernobs.StatusFailed,
		WallTimeMs: 0, QueueWaitMs: 0,
	}); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got := testutil.CollectAndCount(duration); got != 0 {
		t.Errorf("duration metric emissions = %d, want 0 for zero wall time", got)
	}
	if got := testutil.CollectAndCount(queueWait); got != 0 {
		t.Errorf("queue-wait metric emissions = %d, want 0 for zero queue wait", got)
	}
	if got := testutil.ToFloat64(retries.WithLabelValues("stock.run")); got != 0 {
		t.Errorf("retries = %v, want 0", got)
	}
}

// TestRunReportsCollector_UnknownJobType pins the fallback label: an
// empty JobType is labelled "unknown" instead of leaking an empty
// label value.
func TestRunReportsCollector_UnknownJobType(t *testing.T) {
	duration, queueWait, retries := newTestRunReportVectors()
	c := newRunReportsCollectorWithVectors(duration, queueWait, retries)

	if err := c.Collect(context.Background(), &kernobs.RunReport{
		JobID: "job-3", Status: kernobs.StatusSucceeded, WallTimeMs: 1000,
	}); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if _, err := duration.GetMetricWithLabelValues("unknown", "SUCCEEDED"); err != nil {
		t.Errorf("expected label fallback to 'unknown': %v", err)
	}
}

// TestRunReportsCollector_NilSafety pins the nil-tolerant posture: a
// nil receiver and a nil report are safe no-ops.
func TestRunReportsCollector_NilSafety(t *testing.T) {
	var c *RunReportsCollector
	if err := c.Collect(context.Background(), &kernobs.RunReport{JobType: "x"}); err != nil {
		t.Errorf("nil receiver Collect: %v", err)
	}
	cc := NewRunReportsCollector()
	if err := cc.Collect(context.Background(), nil); err != nil {
		t.Errorf("nil report Collect: %v", err)
	}
}
