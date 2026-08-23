// Package delivery — drive_validator_metrics.go (P1.4, July 2026).
//
// DriveValidatorMetrics groups the Prometheus collectors that
// surface the StartupDriveRootsValidator for SRE. It is the
// application-layer handle that promotes the observability package
// vars (defined in metrics_delivery.go) into something the
// validator can consume via dependency injection.
//
// Per AGENTS.md Pattern 0, the struct is composition-injected
// (NewDriveRootsValidator(reg, folders, log, metrics)) rather
// than package-globally imported: production cables wire
// NewDriveValidatorMetrics() against the promauto globals, while
// tests construct a DriveValidatorMetrics from prometheus.NewCounter
// / NewCounterVec registered against a private registry so they do
// not pollute DefaultGatherer. The voiceover/orphan_sweeper.go
// struct-wrapper pattern is the canonical precedent.
//
// nil-safety: the struct pointer may be nil. observeProbe and
// observeRunEnd short-circuit when called on a nil receiver —
// composition roots that pre-date the metrics surface (or want to
// run the validator in a stats-disabled environment) can pass nil
// without guard boilerplate at the call site.
package delivery

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// DriveValidatorMetrics groups the Prometheus collectors that
// StartupDriveRootsValidator emits. The pointer is injected via
// NewDriveRootsValidator; passing nil disables metrics emission
// entirely (no-op observers).
//
// Field visibility is intentional: tests may construct a struct
// literal with locally-registered collectors (NOT promauto)
// wrapping a private registry, then assert increments / histogram
// observations via testutil.ToFloat64.
//
// Production construction: NewDriveValidatorMetrics() backs the
// fields with the promauto globals from
// internal/infrastructure/observability/metrics_delivery.go — those
// globals auto-register with prometheus.DefaultRegisterer and are
// surfaced via api/routes.go::/metrics (promhttp.Handler()).
type DriveValidatorMetrics struct {
	// Probes is the counter incremented per probe attempt. Labels:
	// destination (canonical DestinationKey), outcome
	// (success | failure | skipped). Cardinality bounded at
	// 9 × 3 = 27 series.
	Probes *prometheus.CounterVec

	// Duration is the histogram observed per probe attempt.
	// Same labels as Probes. Buckets cover sub-100ms network
	// probes up to 30s retry-saturated probes.
	Duration *prometheus.HistogramVec

	// LastRunTS is the timestamp (unix seconds) of the most
	// recent validator execution. Single-value gauge —
	// no labels needed.
	LastRunTS prometheus.Gauge

	// LastRunOK is the latched binary view (1=clean, 0=with
	// failures) of the most recent run. Single-value gauge.
	LastRunOK prometheus.Gauge
}

// observeProbe records a single probe outcome (counter increment +
// histogram observation). Safe to call on a nil receiver structurally
// (m == nil short-circuits the whole call); per-field nil guards
// run independently so a partial struct (e.g. only Probes wired, no
// Duration) still increments the wired collector while skipping the
// missing one — this matches TestDriveValidatorMetrics_NilReceiverNoOp_P1_4
// which exercises the partial-struct path to lock the no-regression
// contract for test doubles / future drift where one collector might
// be added before another.
//
// destination MUST be the canonical DestinationKey (string-convert
// via `string(key)`); outcome MUST be one of "success", "failure",
// "skipped" — caller-side enforcement only via the constructor's
// comment block; mis-spelled values create new time-series silently
// (a textbook Prometheus footgun, so the convention is documented
// near the call site in startup_validator.go::ValidateDriveRoots).
func (m *DriveValidatorMetrics) observeProbe(destination, outcome string, elapsed time.Duration) {
	if m == nil {
		return
	}
	if m.Probes != nil {
		m.Probes.WithLabelValues(destination, outcome).Inc()
	}
	if m.Duration != nil {
		m.Duration.WithLabelValues(destination, outcome).Observe(elapsed.Seconds())
	}
}

// observeRunEnd records the validator run summary (last-run
// timestamp + success indicator). Safe to call on a nil receiver
// (m == nil short-circuits); per-gauge nil guards run independently
// so a partial struct (e.g. only LastRunTS wired) still updates the
// wired gauge while skipping the missing one. Same pattern as
// observeProbe.
//
// Caller passes the unix timestamp as a float64; the package does
// not import time.Now() here to preserve testability (tests can
// pin a stable timestamp when asserting the gauge value).
func (m *DriveValidatorMetrics) observeRunEnd(succeeded bool, timestampSeconds float64) {
	if m == nil {
		return
	}
	if m.LastRunTS != nil {
		m.LastRunTS.Set(timestampSeconds)
	}
	if m.LastRunOK != nil {
		if succeeded {
			m.LastRunOK.Set(1)
		} else {
			m.LastRunOK.Set(0)
		}
	}
}
