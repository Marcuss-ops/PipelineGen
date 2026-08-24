package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"go.uber.org/zap"
)

// TestWorkerMetricsRegistered verifies each worker_* metric is
// registered with the Prometheus default registerer and surfaced
// through Gather() under the canonical name.
//
// IMPORTANT: a fresh GaugeVec/CounterVec with NO labels set yet is
// registered as a Collector but contributes ZERO metric rows when
// Gather() is called. We therefore seed each metric with a single
// placeholder labeled row before gathering so the family appears in
// the gather output, then immediately delete it so the placeholder
// does not leak into the production /metrics endpoint.
//
// Failure mode: a typo in a metric Name string or a missing registration
// is caught here immediately rather than waiting for Prometheus scraping
// to fail in staging.
func TestWorkerMetricsRegistered(t *testing.T) {
	// warmup calls register a single placeholder labeled row per metric
	// so the family name appears in Gather() output. Running these
	// against the real metric vars instead of a synthetic registry
	// also doubles as a "labels accepted" smoke test.
	warmup := []struct {
		name string
		seed func()
		del  func()
	}{
		{
			name: "worker_session_active",
			seed: func() { WorkerSessionActive.WithLabelValues("__test__", "production").Set(0) },
			del:  func() { WorkerSessionActive.DeleteLabelValues("__test__", "production") },
		},
		{
			name: "worker_heartbeat_age_seconds",
			seed: func() { WorkerHeartbeatAgeSeconds.WithLabelValues("__test__").Set(0) },
			del:  func() { WorkerHeartbeatAgeSeconds.DeleteLabelValues("__test__") },
		},
		{
			name: "worker_active_tasks",
			seed: func() { WorkerActiveTasks.WithLabelValues("__test__", "_").Set(0) },
			del:  func() { WorkerActiveTasks.DeleteLabelValues("__test__", "_") },
		},
		{
			name: "worker_task_slots",
			seed: func() { WorkerTaskSlots.WithLabelValues("__test__").Set(0) },
			del:  func() { WorkerTaskSlots.DeleteLabelValues("__test__") },
		},
		{
			name: "worker_cpu_utilization_ratio",
			seed: func() { WorkerCPUUtilizationRatio.WithLabelValues("__test__").Set(0) },
			del:  func() { WorkerCPUUtilizationRatio.DeleteLabelValues("__test__") },
		},
		{
			name: "worker_memory_rss_bytes",
			seed: func() { WorkerMemoryRSSBytes.WithLabelValues("__test__").Set(0) },
			del:  func() { WorkerMemoryRSSBytes.DeleteLabelValues("__test__") },
		},
		{
			name: "worker_memory_used_bytes",
			seed: func() { WorkerMemoryUsedBytes.WithLabelValues("__test__").Set(0) },
			del:  func() { WorkerMemoryUsedBytes.DeleteLabelValues("__test__") },
		},
		{
			name: "worker_disk_free_bytes",
			seed: func() { WorkerDiskFreeBytes.WithLabelValues("__test__", "/").Set(0) },
			del:  func() { WorkerDiskFreeBytes.DeleteLabelValues("__test__", "/") },
		},
		{
			name: "worker_temp_bytes",
			seed: func() { WorkerTempBytes.WithLabelValues("__test__").Set(0) },
			del:  func() { WorkerTempBytes.DeleteLabelValues("__test__") },
		},
		{
			name: "worker_network_rx_bytes",
			seed: func() { WorkerNetworkRxBytes.WithLabelValues("__test__", "lo").Set(0) },
			del:  func() { WorkerNetworkRxBytes.DeleteLabelValues("__test__", "lo") },
		},
		{
			name: "worker_network_tx_bytes",
			seed: func() { WorkerNetworkTxBytes.WithLabelValues("__test__", "lo").Set(0) },
			del:  func() { WorkerNetworkTxBytes.DeleteLabelValues("__test__", "lo") },
		},
		{
			name: "worker_reconnect_total",
			seed: func() { WorkerReconnectTotal.WithLabelValues("initial").Inc() },
			del:  func() { WorkerReconnectTotal.DeleteLabelValues("initial") },
		},
		{
			name: "worker_lease_renewal_failures_total",
			seed: func() { WorkerLeaseRenewalFailuresTotal.WithLabelValues("test_reason").Inc() },
			del:  func() { WorkerLeaseRenewalFailuresTotal.DeleteLabelValues("test_reason") },
		},
		{
			name: "worker_artifact_upload_failures_total",
			seed: func() { WorkerArtifactUploadFailuresTotal.WithLabelValues("test_reason").Inc() },
			del:  func() { WorkerArtifactUploadFailuresTotal.DeleteLabelValues("test_reason") },
		},
		{
			name: "worker_fallback_total",
			seed: func() { WorkerFallbackTotal.WithLabelValues("test_kind").Inc() },
			del:  func() { WorkerFallbackTotal.DeleteLabelValues("test_kind") },
		},
		{
			name: "worker_emergency_path_total",
			seed: func() { WorkerEmergencyPathTotal.WithLabelValues("test_kind").Inc() },
			del:  func() { WorkerEmergencyPathTotal.DeleteLabelValues("test_kind") },
		},
		// FASE 0.2 (July 4 2026) silent-drop counters per
		// PR-GODOBJ-14-WORKER-REGISTRY. The 3 new metrics surface
		// telemetry-emit failures that were previously silent-dropped
		// via `_ = X(...)` patterns in worker/*.go. Cardinality bound
		// is enforced by the (job_type, outcome|reason) label tuples;
		// worker_id dimension is intentionally absent (see godlike/06
		// SSOT comment block in worker_metrics.go).
		{
			name: "worker_progress_emitted_total",
			seed: func() { WorkerProgressEmittedTotal.WithLabelValues("__test__", "success").Inc() },
			del:  func() { WorkerProgressEmittedTotal.DeleteLabelValues("__test__", "success") },
		},
		{
			name: "worker_progress_errors_total",
			seed: func() { WorkerProgressErrorsTotal.WithLabelValues("__test__", "broker_emit_failed").Inc() },
			del:  func() { WorkerProgressErrorsTotal.DeleteLabelValues("__test__", "broker_emit_failed") },
		},
		{
			name: "worker_event_drops_total",
			seed: func() { WorkerEventDropsTotal.WithLabelValues("__test__").Inc() },
			del:  func() { WorkerEventDropsTotal.DeleteLabelValues("__test__") },
		},
	}

	found := make(map[string]bool, len(warmup))
	for _, w := range warmup {
		// Always clean up; if the seed itself fails the test won't
		// mistakenly leak series into subsequent runs.
		defer w.del()
		w.seed()
	}

	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		for _, w := range warmup {
			if mf.GetName() == w.name {
				found[w.name] = true
			}
		}
	}
	for _, w := range warmup {
		if !found[w.name] {
			t.Errorf("metric %q not registered with prometheus.DefaultGatherer", w.name)
		}
	}
}

// TestWorkerSessionActive_SetAndRead confirms a Gauge.Set on a
// freshly registered worker_id can be read back via Gather() with the
// expected value. Catches label-key typos before they reach /metrics.
func TestWorkerSessionActive_SetAndRead(t *testing.T) {
	WorkerSessionActive.WithLabelValues("test-worker", "production").Set(1)
	defer WorkerSessionActive.DeleteLabelValues("test-worker", "production")

	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "worker_session_active" {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelsMatch(m.GetLabel(), map[string]string{
				"worker_id":   "test-worker",
				"environment": "production",
			}) {
				if v := m.GetGauge().GetValue(); v != 1 {
					t.Errorf("worker_session_active gauge = %v, want 1", v)
				}
				return
			}
		}
	}
	t.Fatal("worker_session_active{worker_id=test-worker, environment=production} not found in gather output")
}

// TestWorkerFallbackTotal_Inc confirms a Counter.Inc increments
// become visible in Gather() output.
func TestWorkerFallbackTotal_Inc(t *testing.T) {
	WorkerFallbackTotal.WithLabelValues("test_kind").Inc()
	defer WorkerFallbackTotal.DeleteLabelValues("test_kind")

	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "worker_fallback_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelsMatch(m.GetLabel(), map[string]string{"kind": "test_kind"}) {
				if v := m.GetCounter().GetValue(); v != 1 {
					t.Errorf("worker_fallback_total = %v, want 1", v)
				}
				return
			}
		}
	}
	t.Fatal("worker_fallback_total{kind=test_kind} not found in gather output")
}

// TestWithWorker_SkipOnEmpty confirms WithWorker returns the input
// logger unchanged when both worker_id and session_id are empty —
// the helper exists for ergonomic invocation from production handlers
// that pass through optional context.
func TestWithWorker_SkipOnEmpty(t *testing.T) {
	base := zap.NewNop()
	got := WithWorker(base, "", "")
	if got != base {
		t.Error("WithWorker(base, \"\", \"\") should return base logger verbatim")
	}
}

// TestWithWorker_NonEmptyReturnsDifferent confirms WithWorker
// returns a NEW logger when at least one ID is non-empty. We assert
// via zap's pointer identity (zap.Logger.With always returns a new
// pointer) — the test catches "I forgot to wrap" regressions.
func TestWithWorker_NonEmptyReturnsDifferent(t *testing.T) {
	base := zap.NewNop()
	got := WithWorker(base, "worker-01", "sess-abc")
	if got == nil || got == base {
		t.Error("WithWorker(base, non-empty) should produce a derived logger")
	}
}

// TestLogKeyConstantsStable pins the canonical field names. Changing
// them is a BREAKING change for log shippers and dashboards — a test
// fails immediately on rename.
func TestLogKeyConstantsStable(t *testing.T) {
	want := map[string]string{
		"worker_id":      LogKeyWorkerID,
		"session_id":     LogKeySessionID,
		"job_id":         LogKeyJobID,
		"task_id":        LogKeyTaskID,
		"attempt_id":     LogKeyAttemptID,
		"correlation_id": LogKeyCorrelationID,
	}
	for expected, actual := range want {
		if actual != expected {
			t.Errorf("log key drift: want %q, got %q", expected, actual)
		}
	}
}

// labelsMatch is a small helper for label-map comparisons. Iterates
// the slice and pops matching pairs from want; returns true when
// want is empty after iteration.
func labelsMatch(got []*dto.LabelPair, want map[string]string) bool {
	for _, p := range got {
		v, ok := want[p.GetName()]
		if !ok {
			continue
		}
		if v != p.GetValue() {
			return false
		}
		delete(want, p.GetName())
	}
	return len(want) == 0
}
