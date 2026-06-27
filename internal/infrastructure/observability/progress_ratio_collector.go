package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// jobProgressRatioMetric / jobProgressRatioHelp (verbatim constants used
// both by the collector and the unit test for help-text parity — see
// progress_ratio_collector_test.go).
const (
	jobProgressRatioMetric = "job_progress_ratio"
	// View-sync with the long-form Help on the collector below.
	jobProgressRatioHelp = "Ratio of job_progress_events_total / job_progress_calls_total, computed at scrape time. " +
		"Reads 1.0 when no coalescing is active (every call produces an event). " +
		"Reads strictly below 1.0 once coalescing is reducing event pressure. " +
		"Reads 0 when no calls have been received since process start " +
		"(avoids Go division-by-zero; front the 0 with a non-zero calls-counter " +
		"check before drawing dashboard conclusions)."
)

// jobProgressRatioCollector IS the canonical "computed at scrape time"
// Gauge pattern: an arbitrary value derived from sibling counters, exposed
// through a custom prometheus.Collector so that the ratio lives on /metrics
// alongside the source counters without producing two independent
// counter-series queries in the dashboard JSON.
//
// Why a custom Collector instead of a periodic goroutine that sets a
// promauto.NewGauge:
//
//   - A periodic gauge would lag behind the source counters by up to one
//     tick (the goroutine's wake period). The custom Collector computes
//     the ratio at scrape time, so the value is always in lock-step
//     with whatever other counters Prometheus samples in the same scrape.
//   - A periodic gauge would need a separate tear-down story
//     (goroutine leak on shutdown). Custom Collectors are scrape-only:
//     no goroutine, no leak.
//   - The custom Collector pattern is the canonical Prometheus idiom for
//     "derived metrics" (see client_golang MetricAggregator godoc; the
//     same MustNewConstMetric + Emit pattern is recommended for ratios,
//     quantile summaries, and any other value-not-sampled).
//
// Reading the file:
//
//   - jobProgressRatioCollector struct : holds the two source counters
//     and the metric Desc; constructed via newJobProgressRatioCollector.
//   - Describe(ch)                    : emits the Desc; tells the
//     registry "this Collector produces exactly one metric".
//   - Collect(ch)                     : reads Counter.Get()-equivalent
//     on both sources via the dto Write path (see counterValue below),
//     computes the ratio, and emits a single MustNewConstMetric
//     with GaugeValue. No internal state.
//   - package-level init()           : registers the canonical instance
//     with prometheus.DefaultRegisterer via MustRegister.
//
// The two source counters are passed in via the constructor (NOT
// hardcoded to the package-level JobProgress*Total globals) so unit
// tests can build a pristine Collector backed by fresh counters and
// assert the exposition format without polluting the global state of
// process-level metrics.
type jobProgressRatioCollector struct {
	calls  prometheus.Counter
	events prometheus.Counter
	desc   *prometheus.Desc
}

// newJobProgressRatioCollector wires a fresh Collector around the two
// source counters. Production wiring: see the package-level
// jobProgressRatioCollectorSingleton variable below. Test wiring: see
// progress_ratio_collector_test.go (each testcase builds a Collector
// against fresh counters via the same constructor).
func newJobProgressRatioCollector(calls, events prometheus.Counter) *jobProgressRatioCollector {
	return &jobProgressRatioCollector{
		calls:  calls,
		events: events,
		desc: prometheus.NewDesc(
			jobProgressRatioMetric,
			jobProgressRatioHelp,
			nil, // no variable labels: ratio is global to the process
			nil, // no constant labels
		),
	}
}

// Describe is part of the prometheus.Collector interface. Emits exactly
// one Desc — the registry learns that this Collector publishes one
// metric so it can validate cross-Collector uniqueness at registration
// time.
func (c *jobProgressRatioCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
}

// Collect is part of the prometheus.Collector interface. Called by the
// Prometheus registry on every scrape. Reads the cumulative values out
// of the two source counters via counterValue (see below), computes the
// ratio, and emits a single MustNewConstMetric with GaugeValue.
//
// Division-by-zero handling:
//
//   - When calls == 0 (process just started, no Progress calls yet),
//     the ratio is undefined. Emit 0 + document in the help text.
//     Dashboards reading 0 must cross-check
//     rate(job_progress_calls_total[5m]) before drawing conclusions —
//     0 with no calls-rate = "data not yet available", 0 with a
//     positive calls-rate = "every event coalesced away" (a regression
//     signal per PR-Progress / ADR-0002 §D6.4 invariants).
//
// `events > calls` is impossible in healthy operation (each event is
// downstream of ≥1 coalesce-window call): the ratio clamp at 1.0 in
// that pathological case is the safer choice — surfacing a value >1.0
// would invite false-positive "coalescer broken" alerts in dashboards.
//
// Note on Counter.Value(): the prometheus.Counter interface (an
// interface type) does NOT declare a Value() method — the "Value()"
// accessor exists only on the concrete *counter struct. The canonical,
// public-API way to read a Counter's current value programmatically is
// to Write() it into a *dto.Metric proto and pull GetCounter().GetValue()
// — counterValue() below wraps that pattern.
func (c *jobProgressRatioCollector) Collect(ch chan<- prometheus.Metric) {
	callsValue := counterValue(c.calls)
	eventsValue := counterValue(c.events)

	var ratio float64
	if callsValue <= 0 {
		// calls == 0 (process just started) OR the safety fallback for an
		// unexpected Write() error. Emit 0 = "no data yet" per the help text.
		ratio = 0
	} else {
		ratio = eventsValue / callsValue
		if ratio > 1.0 {
			// events > calls is impossible in healthy operation (clamp).
			ratio = 1.0
		}
	}

	ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, ratio)
}

// counterValue reads the cumulative float64 value out of a
// prometheus.Counter via the dto Write path. Returns 0 if the Write
// errors (the only realistic error is a nil receiver, which the
// production wiring does not exhibit). Promoted to a helper so tests
// don't repeat the pb-dto dance and so production + test agree on the
// exact extraction contract.
func counterValue(c prometheus.Counter) float64 {
	pb := &dto.Metric{}
	if err := c.Write(pb); err != nil {
		return 0
	}
	return pb.GetCounter().GetValue()
}

// jobProgressRatioCollectorSingleton is the canonical singleton
// Collector, wired to the package-level Counter globals declared in
// metrics.go. The var declaration is order-safe: Go guarantees that
// JobProgressEventsTotal and JobProgressCallsTotal are initialised
// before jobProgressRatioCollectorSingleton since the constructor call
// references them by name and metrics.go is in the same package as
// this file. init() registers the singleton with
// prometheus.DefaultRegisterer; MustRegister panics on duplicate
// registration, which is what we want: if someone accidentally
// registers the Collector twice (e.g. from a test that already
// registered it), the process dies loudly instead of silently emitting
// duplicate metrics.
var jobProgressRatioCollectorSingleton = newJobProgressRatioCollector(
	JobProgressCallsTotal,
	JobProgressEventsTotal,
)

func init() {
	prometheus.MustRegister(jobProgressRatioCollectorSingleton)
}
