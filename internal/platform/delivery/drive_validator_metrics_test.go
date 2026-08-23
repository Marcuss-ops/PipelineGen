// Package delivery — drive_validator_metrics_test.go (P1.4, July 2026).
//
// Tests for the DriveValidatorMetrics struct wrapper. They verify:
//
//	(1) observeProbe increments the probes counter with correct
//	    labels (destination, outcome) and observes the histogram
//	    with the elapsed duration in seconds.
//	(2) observeRunEnd sets the run-summary gauges (timestamp +
//	    success indicator).
//	(3) Nil-receiver tolerance — both helpers short-circuit when
//	    called on a nil *DriveValidatorMetrics (Composition-time
//	    fail-closed surface: composition roots can pass nil to
//	    disable metrics without guard boilerplate at call sites).
//	(4) Integration with DriveRootsValidator.ValidateDriveRoots:
//	    constructing a private-registry metrics struct, running
//	    a fake-probe validator, and asserting the counter /
//	    histogram / gauge values match the simulated outcomes.
//
// Tests construct their own proma-free collectors
// (prometheus.NewCounterVec / NewHistogramVec / NewGauge) and
// register them against prometheus.DefaultRegisterer — same
// pattern as voiceover/orphan_sweeper_test.go and
// observability/worker_metrics_test.go. The helpers
// `prometheus.MustRegister` / `prometheus.NewCounterVec` are
// require'd-on-call-side to keep the test surface compact.
package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/zap"
)

// newTestMetrics constructs a DriveValidatorMetrics backed by
// prometheus.New* (NOT promauto) collectors. This avoids polluting
// prometheus.DefaultGatherer — tests assert increments via
// testutil.ToFloat64 against the locally-registered collectors.
// Each test allocates its own metrics struct so concurrent test
// runs do not interfere on shared Counter / Histogram state.
func newTestMetrics() *DriveValidatorMetrics {
	reg := prometheus.NewRegistry() // unused — tests reach the *Vec directly
	_ = reg
	return &DriveValidatorMetrics{
		Probes: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "test_probes_total", Help: "test"},
			[]string{"destination", "outcome"},
		),
		Duration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "test_probe_duration_seconds",
				Help:    "test",
				Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
			},
			[]string{"destination", "outcome"},
		),
		LastRunTS: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "test_last_run_timestamp_seconds", Help: "test",
		}),
		LastRunOK: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "test_last_run_succeeded", Help: "test",
		}),
	}
}

// ── Test 1: observeProbe increments counter + histogram ──────────

func TestDriveValidatorMetrics_ObserveProbe_P1_4(t *testing.T) {
	m := newTestMetrics()
	m.observeProbe("image", "success", 250*time.Millisecond)
	m.observeProbe("image", "success", 350*time.Millisecond)
	m.observeProbe("voiceover", "failure", 1*time.Second)

	if got := testutil.ToFloat64(m.Probes.WithLabelValues("image", "success")); got != 2 {
		t.Errorf("image/success counter = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.Probes.WithLabelValues("voiceover", "failure")); got != 1 {
		t.Errorf("voiceover/failure counter = %v, want 1", got)
	}
	if got := testutil.CollectAndCount(m.Duration); got != 2 {
		t.Errorf("histogram series count = %v, want 2 (image/success + voiceover/failure)", got)
	}
}

// ── Test 2: observeRunEnd sets the summary gauges ────────────────

func TestDriveValidatorMetrics_ObserveRunEnd_P1_4(t *testing.T) {
	m := newTestMetrics()

	m.observeRunEnd(true, 123_456_789.0)
	if got := testutil.ToFloat64(m.LastRunTS); got != 123_456_789.0 {
		t.Errorf("LastRunTS = %v, want 123456789", got)
	}
	if got := testutil.ToFloat64(m.LastRunOK); got != 1 {
		t.Errorf("LastRunOK (success) = %v, want 1", got)
	}

	m.observeRunEnd(false, 123_456_790.0)
	if got := testutil.ToFloat64(m.LastRunTS); got != 123_456_790.0 {
		t.Errorf("LastRunTS after second call = %v, want 123456790", got)
	}
	if got := testutil.ToFloat64(m.LastRunOK); got != 0 {
		t.Errorf("LastRunOK (after failure) = %v, want 0", got)
	}
}

// ── Test 3: nil receiver is silently no-op ───────────────────────

func TestDriveValidatorMetrics_NilReceiverNoOp_P1_4(t *testing.T) {
	var m *DriveValidatorMetrics // nil pointer
	// None of these should panic.
	m.observeProbe("image", "success", 100*time.Millisecond)
	m.observeRunEnd(true, 42.0)

	// Also tolerate a struct with nil fields (production wired
	// partially — e.g. only Probes, no Duration).
	partial := &DriveValidatorMetrics{
		Probes: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "test_probes_partial_total", Help: "test"},
			[]string{"destination", "outcome"},
		),
		// Duration, LastRunTS, LastRunOK all nil.
	}
	partial.observeProbe("image", "success", 100*time.Millisecond) // Probes still fires
	if got := testutil.ToFloat64(partial.Probes.WithLabelValues("image", "success")); got != 1 {
		t.Errorf("partial.Probes increment = %v, want 1", got)
	}
	partial.observeRunEnd(true, 42.0) // LastRunTS / LastRunOK nil → no-op
}

// ── Test 4: validator integration under fake probe ──────────────
//
// Drives a Validator where one destination succeeds, one fails,
// and one is skipped (Artlist has empty root in startupEmptyRootRegistry).
// Asserts every label transition gets reflected in the counter +
// histogram, and that the run-summary gauges latch with the correct
// success indicator.
//
// IMPORTANT: skipped rows DO enter the Probes counter with
// outcome="skipped" + elapsed=0 — this lets dashboards distinguish
// "intentionally disabled via empty RootFolderID" from "configured
// but broken via probe failure". See metrics_delivery.go::DriveRootsValidatorProbesTotal
// for the cardinality rationale.

func TestDriveRootsValidator_MetricsIntegration_P1_4(t *testing.T) {
	// startupEmptyRootRegistry has ArtlistRootFolder="" so Artlist
	// surfaces in the Skipped path — exercising both success and
	// failure AND skipped outcomes side-by-side.
	reg := startupEmptyRootRegistry()
	probe := &fakeStartupRootsProbe{
		probeErrFn: func(rootID string) error {
			// Voiceover helper returns MediaRootFolder fallback; with
			// MediaRootFolder="" in startupEmptyRootRegistry, the
			// resolved voiceover folder is "vo-root".
			if rootID == "vo-root" {
				return errors.New("drive: voiceover unreachable")
			}
			return nil
		},
	}
	//   - image     → "images-root" → success
	//   - artlist   → empty RootFolderID → skipped (counter emits!)
	//   - voiceover → "vo-root" + probeErrFn → failure
	//   - the remaining 6 → success
	metrics := newTestMetrics()

	v, err := NewDriveRootsValidator(reg, probe, zap.NewNop(), metrics)
	if err != nil {
		t.Fatalf("NewDriveRootsValidator: %v", err)
	}

	report, valErr := v.ValidateDriveRoots(context.Background())
	if valErr == nil {
		t.Fatal("ValidateDriveRoots: expected ErrDriveStartupValidationFailed (voiceover failed)")
	}
	if !errors.Is(valErr, ErrDriveStartupValidationFailed) {
		t.Errorf("valErr = %v, want chain to ErrDriveStartupValidationFailed", valErr)
	}
	if report == nil {
		t.Fatal("ValidateDriveRoots: report nil despite error")
	}

	// One Probes series materializes per registry.Keys() — the
	// validator emits observeProbe on every iteration step
	// (skipped, success, failure alike) so dashboards can
	// distinguish intentionally-disabled (skipped) from
	// configured-but-broken (failure). The exact breakdown
	// depends on resolver behaviour for which destinations have
	// empty resolved folders under cfg.Drive = startupEmptyRootRegistry.
	// P0-#1 (July 2026): dynamic count via len(reg.Keys()) keeps
	// the test in lockstep with the registry when new destinations
	// are added (SoundEffectSidecar, Document, ClipMetadata).
	if got := testutil.CollectAndCount(metrics.Probes); got != len(reg.Keys()) {
		t.Errorf("Probes series count = %v, want %d (one per registry.Keys(), dynamic count)",
			got, len(reg.Keys()))
	}

	// Per-key assertions via label-aware testutil.
	if got := testutil.ToFloat64(metrics.Probes.WithLabelValues("voiceover", "failure")); got != 1 {
		t.Errorf("Probes{voiceover,failure} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.Probes.WithLabelValues("image", "success")); got != 1 {
		t.Errorf("Probes{image,success} = %v, want 1", got)
	}
	// Skipped rows DO enter the counter (validator emits
	// v.metrics.observeProbe(destKey, "skipped", 0)) — pin here so a
	// future drift that drops the emit is caught at test time.
	if got := testutil.ToFloat64(metrics.Probes.WithLabelValues("artlist", "skipped")); got != 1 {
		t.Errorf("Probes{artlist,skipped} = %v, want 1 (skipped rows DO enter the counter per validator impl)", got)
	}

	// Run-summary gauges latch as "failure" because voiceover failed.
	if got := testutil.ToFloat64(metrics.LastRunOK); got != 0 {
		t.Errorf("LastRunOK (after failure) = %v, want 0", got)
	}
	if got := testutil.ToFloat64(metrics.LastRunTS); got <= 0 {
		t.Errorf("LastRunTS = %v, want > 0 (unix seconds)", got)
	}
}

// ── Test 5: validator integration, all-pass latches the OK gauge

func TestDriveRootsValidator_MetricsIntegrationAllPass_P1_4(t *testing.T) {
	reg := startupTestRegistry()
	probe := &fakeStartupRootsProbe{} // all roots pass
	metrics := newTestMetrics()

	v, err := NewDriveRootsValidator(reg, probe, zap.NewNop(), metrics)
	if err != nil {
		t.Fatalf("NewDriveRootsValidator: %v", err)
	}

	_, valErr := v.ValidateDriveRoots(context.Background())
	if valErr != nil {
		t.Fatalf("ValidateDriveRoots: all-pass should err=nil, got %v", valErr)
	}

	if got := testutil.ToFloat64(metrics.LastRunOK); got != 1 {
		t.Errorf("LastRunOK (all-pass) = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.LastRunTS); got <= 0 {
		t.Errorf("LastRunTS = %v, want > 0", got)
	}
}
