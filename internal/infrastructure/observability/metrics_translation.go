// Package observability — metrics_translation.go: typed-port adapter for
// the script translation postprocessor warnings metric.
//
// PR-TRANSLATE-SCRIPT-SPEC forward-pointer FP3 (2026-08-08): the
// application-layer TranslationProcessor calls this adapter through
// the scripts.TranslationMetricsRecorder interface (re-export shim of
// ports/metrics.TranslationMetricsRecorder); the adapter wraps the
// Prometheus counter `script_translation_warnings_total` (now
// per-adapter: each NewTranslationMetricsAdapter(reg) instance holds
// its own CounterVec registered to the provided prometheus.Registerer,
// so production uses prometheus.DefaultRegisterer and tests use
// prometheus.NewRegistry() for hermetic counter assertions).
//
// godlike/06 SSOT (one canonical owner per fact): the
// TranslationMetricsAdapter is the SOLE canonical writer of the
// script_translation_warnings_total counter on the
// application-side wiring. The counter definition now lives HERE
// (per-adapter) instead of in metrics_jobs.go (package-global
// promauto) so the metrics layer is hermetic-testable without
// touching the default Prometheus registry.
//
// godlike/07 typed-error contract: the targetLang parameter is
// normalized via metrics.NormalizeTargetLang to a bounded set (per
// the cardinality guard — the empty string maps to "unknown", any
// other high-cardinality token is preserved as-is for operator
// visibility); the reason parameter is the BOUNDED typed
// metrics.TranslationWarningReason enum (defined in the leaf
// sub-package internal/capabilities/scripts/ports/metrics). The
// bounded enum prevents label-cardinality explosion in Prometheus
// (gofake-style N×M combinations would otherwise blow up the
// Prometheus storage). A free-form reason string is no longer
// possible at the metric-write boundary — only the canonical 10
// bounded values can flow through the typed port.
//
// CYCLE-BREAKER (PR-TRANSLATE-SCRIPT-SPEC FP3 closure, 2026-08-08):
// the typed enum + port + helper live in the leaf sub-package
// `internal/capabilities/scripts/ports/metrics` (ZERO upward
// imports). observability imports this leaf directly (not the
// parent `ports` package) so the cycle
// `observability → ports → jobs → sqlite/jobs → observability` is
// broken. The ports package keeps a thin re-export shim
// (`type TranslationWarningReason = metrics.TranslationWarningReason`)
// so the 8 existing call sites in usecase/translation.go +
// adapters/processor_translation.go continue to import the canonical
// `ports.TranslationWarningReason` path.
package observability

import (
	"errors"
	"fmt"

	scriptmetrics "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// godlike/07 NO-FAKE-AVAILABILITY (target_lang cardinality):
// the resolved plan language is intentionally OPEN-ended (any
// ISO 639-1 / BCP-47 tag the planner resolves is permitted), so
// the cardinality guard is the bounded reason enum (10 values)
// NOT the language set. The canonical NormalizeTargetLang
// implementation lives in the leaf sub-package
// `internal/capabilities/scripts/ports/metrics`
// (PR-TRANSLATE-SCRIPT-SPEC FP3 cycle-breaker); the metrics
// adapter calls `scriptmetrics.NormalizeTargetLang(lang)` at the
// metric-write boundary so the target_lang label normalization is
// centralized at the port owner (empty/whitespace maps to
// "unknown"; non-empty preserved verbatim for operator visibility).
//
// Historical note: a BoundedSupportedLanguages = []string{...}
// var lived here in an earlier draft. It was REMOVED (PR-TRANSLATE-SCRIPT-SPEC
// cycle-breaker followup, 2026-08-08) because the language set is
// intentionally open — coercing off-list codes would violate
// godlike/07 NO-FAKE-AVAILABILITY (operator loses visibility into
// real-world language distribution). The bounded reason enum
// remains the load-bearing cardinality guard.

// TranslationMetricsAdapter is the typed-port adapter that satisfies
// scripts.TranslationMetricsRecorder. The composition root wires it
// via NewTranslationMetricsAdapter(reg).
//
// godlike/06 SSOT (one canonical owner per fact): each adapter
// instance holds its OWN counter (registered to the prometheus.Registerer
// passed to the ctor). The production composition root uses
// prometheus.DefaultRegisterer so the counter is scrapeable on
// /metrics; tests use prometheus.NewRegistry() for hermetic
// per-test counter assertions (no global state pollution).
//
// godlike/07 NO-FAKE-AVAILABILITY: nil-receiver + nil-receiver
// after construction is silent no-op (the postprocessor's nil-port
// check fires first, so the adapter is never invoked nil, but the
// method itself is nil-safe for defense-in-depth).
type TranslationMetricsAdapter struct {
	registry            *prometheus.Registry
	translationWarnings *prometheus.CounterVec
}

// NewTranslationMetricsAdapter returns the canonical translation
// metrics adapter with a per-adapter prometheus.Registry + CounterVec.
// The composition root passes prometheus.DefaultRegisterer
// (production); tests pass prometheus.NewRegistry() (hermetic).
//
// godlike/07 minimum-blast-radius: the ctor fail-closed at nil
// registry — the postprocessor's nil-port check fires BEFORE
// the adapter is invoked, so a nil-registry ctor is a composition
// bug. The constructor returns a typed error so the composition
// root can wrap it with the "wireScript:" prefix and fail-closed.
func NewTranslationMetricsAdapter(reg prometheus.Registerer) (*TranslationMetricsAdapter, error) {
	if reg == nil {
		return nil, errNilRegistryForTranslationMetrics
	}
	// Per-adapter CounterVec (NOT package-global promauto.NewCounterVec).
	// The counter is registered to the provided Registerer so the
	// composition root can choose between production (default
	// registerer + /metrics scrape) and test (per-test
	// prometheus.NewRegistry() for hermetic assertions).
	cv := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "script_translation_warnings_total",
		Help: "Total number of script translation postprocessor warnings + non-fatal errors, partitioned by target_lang and reason. See PR-TRANSLATE-SCRIPT-SPEC forward-pointer FP3.",
	}, []string{"target_lang", "reason"})
	if err := reg.Register(cv); err != nil {
		// Already-registered: make the adapter idempotent so that
		// multiple WireRegistry invocations in the same process
		// (notably test suites) do not fail. Retrieve the existing
		// CounterVec and reuse it.
		var alreadyReg prometheus.AlreadyRegisteredError
		if errors.As(err, &alreadyReg) {
			existing, ok := alreadyReg.ExistingCollector.(*prometheus.CounterVec)
			if !ok {
				return nil, fmt.Errorf("translation metrics: existing collector is %T, not *prometheus.CounterVec", alreadyReg.ExistingCollector)
			}
			cv = existing
		} else {
			return nil, errTranslationMetricsAlreadyRegistered
		}
	}

	// Internal: hold the registry so Registry() can return it for
	// test assertions. The Registry is what test helpers call
	// .Gather() on to enumerate CounterVec series.
	return &TranslationMetricsAdapter{
		registry:            unwrapRegistry(reg),
		translationWarnings: cv,
	}, nil
}

// errNilRegistryForTranslationMetrics is the typed sentinel
// returned by NewTranslationMetricsAdapter when the composition
// root passes a nil prometheus.Registerer. The composition root
// MUST provide a registry (default or test) — a nil registry
// would mean the metrics are not scrapeable.
var errNilRegistryForTranslationMetrics = &translationMetricsError{msg: "translation metrics: nil prometheus.Registerer (composition bug)"}

// errTranslationMetricsAlreadyRegistered is the typed sentinel
// returned by NewTranslationMetricsAdapter when the CounterVec
// has already been registered to the same Registerer (typically a
// double-wiring in the composition root or a test re-using a
// registry). The composition root should ALWAYS pass a fresh
// Registerer per adapter instance.
var errTranslationMetricsAlreadyRegistered = &translationMetricsError{msg: "translation metrics: counter already registered to the given Registerer (composition bug — pass a fresh Registerer)"}

type translationMetricsError struct{ msg string }

func (e *translationMetricsError) Error() string { return e.msg }

// IncTranslationWarning increments the script_translation_warnings_total
// counter with the given target_lang + bounded reason labels.
// targetLang is the resolved plan language (free-form string,
// normalized via scriptmetrics.NormalizeTargetLang to the "unknown"
// fallback for empty/whitespace); reason is the BOUNDED typed
// scriptmetrics.TranslationWarningReason enum (one of 10 closed
// values; an out-of-bounds value is rejected at compile time by
// the typed port signature, so the runtime cardinality guard is
// belt-and-suspenders).
//
// Nil-receiver tolerant: a nil-receiver method call is a silent
// no-op (the postprocessor's nil-port check fires FIRST, so the
// adapter is never invoked nil, but the method itself is
// nil-safe for defense-in-depth).
//
// PR-TRANSLATE-SCRIPT-SPEC FP3 (2026-08-08): the typed reason
// parameter is `scriptmetrics.TranslationWarningReason` (from the
// leaf sub-package `internal/capabilities/scripts/ports/metrics`),
// NOT `ports.TranslationWarningReason` (the re-export alias). Both
// types are byte-equivalent at compile time (Go type aliases are
// pure rename), but the import path here is the leaf sub-package
// directly to break the observability → ports → jobs →
// sqlite/jobs → observability import cycle.
func (a *TranslationMetricsAdapter) IncTranslationWarning(targetLang string, reason scriptmetrics.TranslationWarningReason) {
	if a == nil || a.translationWarnings == nil {
		return
	}
	normalizedLang := scriptmetrics.NormalizeTargetLang(targetLang)
	// Empty typed reason is impossible (the port signature
	// enforces the typed enum) but defense-in-depth: coerce to
	// ReasonUnknown so the label cardinality is bounded even if
	// a future agent wires a stringly-typed port.
	reasonStr := string(reason)
	if reasonStr == "" {
		reasonStr = string(scriptmetrics.ReasonUnknown)
	}
	a.translationWarnings.WithLabelValues(normalizedLang, reasonStr).Inc()
}

// Registry returns the per-adapter prometheus.Registry for
// hermetic test assertions. Production callers (the /metrics
// handler) use prometheus.DefaultGatherer instead — the Registry()
// accessor is the canonical seam for test surface area (test
// helpers call .Registry().Gather() to enumerate CounterVec
// series).
//
// godlike/07 minimum-blast-radius: nil-receiver returns nil (the
// test helper sees a nil registry and skips assertions per
// standard Go nil-handle semantics).
func (a *TranslationMetricsAdapter) Registry() *prometheus.Registry {
	if a == nil {
		return nil
	}
	return a.registry
}

// unwrapRegistry extracts the *prometheus.Registry from a
// prometheus.Registerer (the Registerer interface is implemented
// by both *prometheus.Registry and *prometheus.DefaultRegisterer;
// for the default registerer, we return nil and the test surface
// uses prometheus.DefaultGatherer instead).
func unwrapRegistry(reg prometheus.Registerer) *prometheus.Registry {
	if r, ok := reg.(*prometheus.Registry); ok {
		return r
	}
	return nil
}
