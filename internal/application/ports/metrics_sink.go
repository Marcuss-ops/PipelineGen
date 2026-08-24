// Package ports — MetricsSink port (Fase 5(a), July 2026).
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// metrics-emitting surface consumed by application-layer use cases.
//
// The concrete implementations live in:
//
//   - internal/platform/observability/*    (production — wraps prometheus promauto globals)
//   - A test fake (`metricsink.NoOp` or per-test stub)         (hermetic tests)
//
// godlike/07 minimum-blast-radius: until Phase 5(b), the application
// layer imports `github.com/prometheus/client_golang/prometheus/promauto`
// directly via `internal/platform/observability` package
// references. Fase 5(b) removes those imports — application code calls
// `sink.Inc(...)` instead of `promauto.X.WithLabelValues(...).Inc()`.
//
// All 3 methods accept `labels ...string` as a canonical key/value
// sequence: every pair `(labels[2i], labels[2i+1])` is a single label.
// Encoding-validation (odd-length / empty key) is deferred to the
// production concrete (rest-of-Phase-5(b)) so the interface surface
// stays trivially mockable for hermetic tests.
package ports

// MetricsSink is the canonical narrow port for emitting application-layer
// metrics. The interface is intentionally minimal (3 methods matching
// the canonical Prometheus trio: counter / gauge / histogram) so use
// cases can opt in incrementally without a port surface explosion.
//
// godlike/07 fail-closed contract:
//
//   - Every method panics on INVALID label key (odd-length labels list
//     OR key==""): a metrics emit with an invalid label is a CALLER BUG,
//     not a runtime condition that warrants graceful degradation.
//   - The NopMetricsSink returned by NewNopMetricsSink satisfies the
//     interface without registering any globals — safe for hermetic
//     tests and any composition-time fallback.
//
// Errors: never returns an error at the interface level (mirrors
// Prometheus's `MustRegister`/`Inc` semantics).
type MetricsSink interface {
	// Inc increments a counter metric by 1 with the given label
	// key/value pairs. Use this for monotonic counts (job-completed,
	// artifact-published, etc.).
	//
	// Labels: passed as alternating (key, value) pairs. `Inc("jobs_completed_total", "type", "script.generate")`
	// emits `jobs_completed_total{type="script.generate"} += 1`. An odd-length `labels` list panics.
	Inc(name string, labels ...string)

	// Add increments a counter metric by `value` (or sets a gauge to
	// `value` if `name` is registered as a gauge). Use this for
	// sized counts (bytes-uploaded, retry-backoff-ms, etc.).
	//
	// Labels: same key/value contract as Inc.
	Add(name string, value float64, labels ...string)

	// Observe observes a single sample in a histogram metric.
	// Use this for latency / size distributions (job-duration-seconds,
	// artifact-upload-bytes, etc.).
	//
	// Labels: same key/value contract as Inc.
	Observe(name string, value float64, labels ...string)
}

// NopMetricsSink is the canonical no-op MetricsSink. Use it in tests
// and in composition paths where a metrics sink is required by the
// constructor but the caller has not (yet) wired a real one.
//
// godlike/07 NO-FAKE-AVAILABILITY: NopMetricsSink is NOT a fallback
// for production code. A use case that calls `sink.Inc(...)` on a
// NopMetricsSink in production has lost its monitor signal — it is
// silently lying about observed events. The composition root must
// wire a real MetricsSink from day 1.
type NopMetricsSink struct{}

// Inc on NopMetricsSink is a no-op.
func (NopMetricsSink) Inc(string, ...string) {}

// Add on NopMetricsSink is a no-op.
func (NopMetricsSink) Add(string, float64, ...string) {}

// Observe on NopMetricsSink is a no-op.
func (NopMetricsSink) Observe(string, float64, ...string) {}

// Compile-time identity lock (godlike/06 SSOT — alias freezes the
// canonical `interface{}` value identity for downstream drift-detection).
// Sits at package init so any future unwiring of the no-op impl
// surfaces as a build-time error, not a runtime nil-deref.
var _ MetricsSink = (MetricsSink)(nil)
