package observability

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/expfmt"
)

// freshCounters returns a (calls, events) pair backed by fresh new
// counters (test-side only — the canonical JobProgress*Total globals
// are mutated through `promauto`, never shared with tests). Each
// test case calls this so counters start at zero, eliminating
// cross-test pollution.
func freshCounters() (prometheus.Counter, prometheus.Counter) {
	calls := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_calls_total", Help: "test"})
	events := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_events_total", Help: "test"})
	return calls, events
}

// deltat is a float-equality helper that allows tiny float-precision
// drift in the ratio computation. Prometheus collectors emit
// float64; YAML/JSON expectations assert exact equality. We tolerate
// an epsilon of 1e-9 to keep tests robust without hiding real bugs.
func deltat(got, want float64) bool {
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	return diff < 1e-9
}

// TestJobProgressRatioCollector_DescribeEmitsExactlyOneMetric locks
// the prometheus.Collector invariant: Describe MUST emit exactly one
// Desc (cannot be 0; cannot be >1 — the registry rejects collectors
// that produce 0 metrics or that produce metric names not preceded
// by Describe). If a future maintainer adds a second metric here, this
// test must FAIL — update it (and the help text + metric constant).
func TestJobProgressRatioCollector_DescribeEmitsExactlyOneMetric(t *testing.T) {
	calls, events := freshCounters()
	c := newJobProgressRatioCollector(calls, events)

	descs := make(chan *prometheus.Desc, 2)
	c.Describe(descs)
	close(descs)

	count := 0
	for range descs {
		count++
	}
	if count != 1 {
		t.Errorf("Describe emitted %d metrics, want exactly 1", count)
	}
}

// TestJobProgressRatioCollector_ZeroWhenNoCalls locks the calls==0
// branch: ratio defaults to 0 with documented "no data yet"
// semantics — the else branch in Collect (callsValue <= 0 → 0).
func TestJobProgressRatioCollector_ZeroWhenNoCalls(t *testing.T) {
	calls, events := freshCounters()
	c := newJobProgressRatioCollector(calls, events)

	got := testutil.ToFloat64(c)
	if got != 0.0 {
		t.Errorf("zero-calls path: got %v, want 0.0", got)
	}
}

// TestJobProgressRatioCollector_NormalRatio locks the canonical
// "coalescing is active" path: 8 events / 10 calls = 0.8. This is the
// steady-state ratio every operator dashboard reads.
func TestJobProgressRatioCollector_NormalRatio(t *testing.T) {
	calls, events := freshCounters()
	calls.Add(10)
	events.Add(8)
	c := newJobProgressRatioCollector(calls, events)

	got := testutil.ToFloat64(c)
	if !deltat(got, 0.8) {
		t.Errorf("normal-ratio path: got %v, want 0.8", got)
	}
}

// TestJobProgressRatioCollector_RatioEqualsUnity locks the perfect
// coalesce-bypass case (no coalescing): 5 events / 5 calls → 1.0.
// Operators expect 1.0 BEFORE the coalescer is enabled.
func TestJobProgressRatioCollector_RatioEqualsUnity(t *testing.T) {
	calls, events := freshCounters()
	calls.Add(5)
	events.Add(5)
	c := newJobProgressRatioCollector(calls, events)

	got := testutil.ToFloat64(c)
	if got != 1.0 {
		t.Errorf("unity path: got %v, want 1.0", got)
	}
}

// TestJobProgressRatioCollector_ClampsAboveUnity locks the
// impossible-in-healthy-operation branch: 8 events / 5 calls (a
// wiring regression in the coalescer). Ratio IS clamped to 1.0 — the
// alternative (emit > 1.0) would invite false-positive "coalescer
// broken" alerts. Documented in Collect's doc comment.
func TestJobProgressRatioCollector_ClampsAboveUnity(t *testing.T) {
	calls, events := freshCounters()
	calls.Add(5)
	events.Add(8) // 8/5 = 1.6 → clamp to 1.0
	c := newJobProgressRatioCollector(calls, events)

	got := testutil.ToFloat64(c)
	if got != 1.0 {
		t.Errorf("clamp-above-unity path: got %v, want 1.0 (clamped)", got)
	}
}

// TestJobProgressRatioCollector_ExpositionFormatLocksHelpAndType
// locks the Prometheus exposition-format contract by hand-rolling
// the registry → gather → MetricFamilyToText path. We do NOT use
// testutil.GatherAndCompare: the version of testutil in this module
// takes a Gatherer (not a Collector), so the call-site signature is
// version-coupled. The hand-rolled path uses only the stable
// prometheus.Collector + expfmt.MetricFamilyToText contract.
//
// Verifies:
//   - Metric TYPE is "gauge" (matches the spec's "Gauge" affordance).
//   - HELP carries the documented help text verbatim.
//   - Metric NAME is the documented constant (no drift between
//     collective-side constant + what /metrics actually emits).
func TestJobProgressRatioCollector_ExpositionFormatLocksHelpAndType(t *testing.T) {
	calls, events := freshCounters()
	calls.Add(10)
	events.Add(8)
	c := newJobProgressRatioCollector(calls, events)

	reg := prometheus.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("Register on fresh registry failed: %v", err)
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather on fresh registry failed: %v", err)
	}
	if len(mfs) != 1 {
		t.Fatalf("Gather produced %d metric families, want exactly 1", len(mfs))
	}

	mf := mfs[0]
	if mf.GetName() != jobProgressRatioMetric {
		t.Errorf("metric family name: got %q, want %q",
			mf.GetName(), jobProgressRatioMetric)
	}

	gaugeType := mf.GetType()
	// MetricType_Gauge == 2 in prometheus/client_model/go; the canonical
	// String() formatter prints "GAUGE". expfmt.MetricFamilyToText emits
	// "# TYPE <name> gauge" (lowercase, per exposition spec §"Text format").
	if got := gaugeType.String(); got != "GAUGE" {
		t.Errorf("metric type: got %s, want GAUGE", got)
	}

	help := mf.GetHelp()
	if help != jobProgressRatioHelp {
		t.Errorf("help text drift:\n  got:  %q\n  want: %q",
			help, jobProgressRatioHelp)
	}

	// And finally: the full text rendering (exposition format) carries the
	// HELP line + the TYPE line + the metric line.
	var buf bytes.Buffer
	if _, err := expfmt.MetricFamilyToText(&buf, mf); err != nil {
		t.Fatalf("MetricFamilyToText failed: %v", err)
	}
	text := buf.String()

	for _, expected := range []string{
		"# HELP " + jobProgressRatioMetric + " " + jobProgressRatioHelp,
		"# TYPE " + jobProgressRatioMetric + " gauge",
		jobProgressRatioMetric + " 0.8",
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("exposition text missing %q\ngot:\n%s", expected, text)
		}
	}

	// Final structural sanity: exactly one Metric on the family. The
	// three Contains checks above already catch all the contract
	// violations we'd care about (drift name / help / type / value),
	// so this is the only structural guard we need.
	if mf.GetMetric() == nil || len(mf.GetMetric()) != 1 {
		t.Errorf("metric family has %d metrics, want 1", len(mf.GetMetric()))
	}
}

// TestJobProgressRatioCollector_FreshRegistryGatherAndExposesScalarValue
// locks the production-scraping contract: when registered on a fresh
// Registry (the Gatherer boundary that promhttp.Handler / /metrics
// uses internally), the scalar value exposed via
// testutil.ToFloat64 matches the computed ratio.
//
// This is the "happy path" that mirrors a real Prometheus scrape
// hitting /metrics and reading the job_progress_ratio line.
func TestJobProgressRatioCollector_FreshRegistryGatherAndExposesScalarValue(t *testing.T) {
	calls, events := freshCounters()
	calls.Add(10)
	events.Add(8)
	c := newJobProgressRatioCollector(calls, events)

	reg := prometheus.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("Register on fresh registry failed: %v", err)
	}

	// testutil.ToFloat64 reads the collector's emitted Metric directly
	// without going through the registry, so this path also implicitly
	// verifies that the Collect() side channel works (gauge emitted,
	// counter values read via dto.Write).
	got := testutil.ToFloat64(c)
	if !deltat(got, 0.8) {
		t.Errorf("fresh-registry probe path: got %v, want 0.8", got)
	}
}

// TestJobProgressRatioCollector_DuplicateRegistrationFails locks the
// metric-name uniqueness invariant that production init() relies on:
// MustRegister panics on duplicate name. We exercise the safe path
// here (Register on a fresh registry) since calling MustRegister on
// the global prometheus.DefaultRegisterer a second time in a test
// would panic the whole test binary.
//
// If the production init() ever drifted into double-registration
// (e.g. by re-running the package init in an unusual binary load
// order) the integration-test side of the codebase would catch the
// panic at startup; this unit test locks the contract that the panic
// IS the right failure mode for that scenario.
func TestJobProgressRatioCollector_DuplicateRegistrationFails(t *testing.T) {
	calls, events := freshCounters()
	c := newJobProgressRatioCollector(calls, events)

	reg := prometheus.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("first Register on fresh registry failed: %v", err)
	}

	dup := newJobProgressRatioCollector(calls, events)
	err := reg.Register(dup)
	if err == nil {
		t.Fatalf("duplicate registration of metric %q should fail; got nil error",
			jobProgressRatioMetric)
	}
	// Tight assertions on the error type: the production init() relies
	// on prometheus.AlreadyRegisteredError being the canonical failure
	// mode, which MustRegister re-panics with. A future Register-wrapper
	// that returns a different error sentinel would silently fall through
	// the production init's panic-recovery path and break the fail-loud
	// guarantee.
	//
	// errors.As (not errors.Is) is used because AlreadyRegisteredError
	// is a struct TYPE returned by value (not a pointer sentinel), and a
	// zero-value target for errors.Is would not match the populated
	// returned error. errors.As works on the dynamic type, so it locks
	// the contract across all client_golang versions regardless of any
	// Is() method presence on the struct.
	var target prometheus.AlreadyRegisteredError
	if !errors.As(err, &target) {
		t.Errorf("duplicate registration error: got %v (%T), want prometheus.AlreadyRegisteredError",
			err, err)
	}
}
