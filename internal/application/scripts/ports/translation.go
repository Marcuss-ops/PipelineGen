// Package scripts — ports/translation.go: typed ports for the
// script translation postprocessor.
//
// PR-TRANSLATE-SCRIPT-SPEC forward-pointer FP2 (2026-08-08): the
// application-layer TranslationProcessor consumes these ports via
// DI; the composition root wires concrete adapters that delegate
// to `root.AI.OllamaTranslator` (the canonical IT↔EN LLM translator
// already wired in build_bundles_voiceover.go for the
// voiceover.Promo surface) + `usecase.TranslateScriptSpec` (the
// pure-function translation surface) + `usecase.ClassifyReason`
// (the bounded-reason classifier). The ports are nil-tolerant so
// the processor degrades gracefully to "postprocessor not wired"
// warning (matches the metadata/clip_search unavailable-adapter
// pattern).
//
// godlike/06 SSOT (one canonical owner per fact): every port in
// this file is a 1-method typed interface (canonical Pattern 0).
// Bare function values are NOT used for DI in this package — every
// collaborator is either a typed port or a noop sentinel.
//
// Three typed surfaces in this file:
//
//  1. ScriptTranslator (LLM call surface) — 1 method, called per
//     text segment by the postprocessor.
//  2. TranslationUseCase (pure translation surface) — 1 method,
//     called once per Process() invocation with the full
//     *ModelScriptOutputV1 envelope.
//  3. TranslationReasonClassifier (bounded-reason classifier) — 1
//     method, called per warning + per typed error.
package ports

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ScriptTranslator is the canonical SOLE consumer surface for the
// script translation LLM call. The composition root wires a
// concrete adapter that satisfies this port (typically a thin
// wrapper around `root.AI.OllamaTranslator.TranslateText` which
// already has the canonical `translation.TranslatorFunc` signature).
//
// godlike/06 SSOT (one canonical owner per fact): this interface
// is the SOLE canonical typed contract for the translation
// postprocessor. Sibling consumers (voiceover.Promo +
// artlist.ClipSearch) use the underlying `translation.TranslatorFunc`
// directly; the postprocessor layer owns the per-target-language
// iteration + SpecScene mutation + warnings channel + metrics
// emission, so the dedicated port is justified per the
// godlike/07 typed-error contract (BoundedReason enum).
//
// godlike/07 NO-FAKE-AVAILABILITY: nil receiver → no-op. A
// composition gap is surfaced as a "translator_missing" warning
// + metric, NOT as a hard failure (the postprocessor policy is
// BestEffort).
type ScriptTranslator interface {
	// Translate translates a single text segment to targetLanguage.
	// Implementations MUST return a non-empty string on success
	// (whitespace-only is treated as empty by the postprocessor).
	Translate(ctx context.Context, text, targetLanguage string) (string, error)
}

// TranslationUseCase is the canonical SOLE typed DI surface for the
// pure translation function `usecase.TranslateScriptSpec`. The
// composition root wires a thin struct adapter (built via
// `usecase.NewTranslationUseCaseAdapter`) that delegates to the
// pure function byte-for-byte.
//
// godlike/06 SSOT (one canonical owner per fact): the postprocessor
// layer does NOT call `usecase.TranslateScriptSpec` directly because
// the `adapters → usecase` import edge is forbidden (cycle via
// (was: `usecase/documents_usecase.go`, retired in Sprint 1.0). The typed port + thin adapter
// pattern is the canonical godlike/06 SSOT solution: the port
// lives in `ports/`, the adapter lives in `usecase/`, the consumer
// lives in `adapters/` — three packages with NO cycle, each owning
// its own canonical concern.
//
// godlike/07 minimum-blast-radius: the port signature matches the
// canonical pure function signature byte-for-byte, so the
// adapter's TranslateScriptSpec method body is a single
// function-delegation line. Adding a new translation field to the
// envelope is a port-method-signature change (forward-prevention:
// compile-time pin in the adapter file surfaces drift as a build
// failure).
type TranslationUseCase interface {
	// TranslateScriptSpec translates the text fields of
	// `in` to `targetLang` while preserving byte-identical
	// identifier-keyed structure. Returns the translated
	// *ModelScriptOutputV1 + non-fatal warnings + a typed error
	// envelope (see the sentinel taxonomy in
	// internal/application/scripts/usecase/translation.go).
	TranslateScriptSpec(
		ctx context.Context,
		in *scriptpkg.ModelScriptOutputV1,
		evidence *scriptpkg.ClipEvidence,
		targetLang string,
		translator ScriptTranslator,
	) (out *scriptpkg.ModelScriptOutputV1, warnings []string, err error)
}

// TranslationReasonClassifier is the canonical SOLE typed DI
// surface for the bounded-reason classifier
// `usecase.ClassifyReason`. The composition root wires a thin
// struct adapter (built via
// `usecase.NewTranslationReasonClassifierAdapter`) that delegates
// to the pure function byte-for-byte.
//
// godlike/06 SSOT (one canonical owner per fact): same rationale
// as TranslationUseCase — the typed port + thin adapter pattern
// breaks the adapters → usecase import cycle while preserving the
// canonical 1-method Pattern 0 port convention.
//
// godlike/07 typed-error contract: the returned enum is the
// canonical bounded `TranslationWarningReason` (this package is
// the SOLE canonical owner). The metrics adapter coerces any
// out-of-bounds value to `ReasonUnknown` at the Prometheus label
// boundary, so the cardinality is guaranteed bounded even if a
// future reason-token slips through.
type TranslationReasonClassifier interface {
	// ClassifyReason returns the bounded TranslationWarningReason
	// for a warning string OR a typed-error .Error()
	// representation. The mapping is exhaustive for the canonical
	// TranslateScriptSpec surface; values NOT matching any of the
	// canonical substring tokens return ReasonUnknown.
	ClassifyReason(s string) TranslationWarningReason
}

// Compile-time pin: the canonical translatorFuncAdapter satisfies
// ScriptTranslator (function-type adapters are the simplest
// concrete impl). This locks the signature contract across
// refactors — a future signature drift in ScriptTranslator
// surfaces as a build failure, not a runtime panic.
var _ ScriptTranslator = translatorFuncAdapter(nil)

// Compile-time pin: noopTranslationUseCase satisfies TranslationUseCase
// (godlike/07 minimum-blast-radius nil-fallback). A nil-translation
// processor emits ReasonTranslatorMissing warnings + metrics.
var _ TranslationUseCase = (*noopTranslationUseCase)(nil)

// Compile-time pin: noopTranslationReasonClassifier satisfies
// TranslationReasonClassifier (godlike/07 minimum-blast-radius
// nil-fallback). A nil-classifier coerces every reason to
// ReasonUnknown so the bounded-reason contract is preserved.
var _ TranslationReasonClassifier = (*noopTranslationReasonClassifier)(nil)

// NewScriptTranslatorFromFunc wraps a `translation.TranslatorFunc`
// as a ScriptTranslator. The composition root uses this to bridge
// the canonical `root.AI.OllamaTranslator` (which is itself a
// `translation.TranslatorFunc`) to the postprocessor's typed port.
//
// godlike/07 minimum-blast-radius: zero-allocation adapter (the
// function value is captured by reference); nil-func returns a
// nil-ScriptTranslator so the postprocessor's nil-tolerance
// degrades to "translator_missing" warning (mirrors the
// unavailable-adapter pattern).
func NewScriptTranslatorFromFunc(fn func(ctx context.Context, text, targetLanguage string) (string, error)) ScriptTranslator {
	if fn == nil {
		return nil
	}
	return translatorFuncAdapter(fn)
}

// NewNoopTranslationUseCase returns the canonical nil-tolerance
// fallback for the TranslationUseCase port. The composition root
// uses this when no real pure-function adapter is wired so the
// script.generate pipeline does not abort on a composition gap.
//
// godlike/07 NO-FAKE-AVAILABILITY: the noop sentinel's
// TranslateScriptSpec method always returns
// (nil, nil, ErrNoopTranslationUseCase) so the postprocessor's
// typed-error classification (classifyFunc) surfaces the canonical
// "translator_missing" or "source_invalid" reason at the metrics
// boundary, NOT a silent success.
func NewNoopTranslationUseCase() TranslationUseCase {
	return &noopTranslationUseCase{}
}

// NewNoopTranslationReasonClassifier returns the canonical
// nil-tolerance fallback for the TranslationReasonClassifier
// port. The composition root uses this when no real classifier is
// wired so the postprocessor's bounded-reason contract is
// preserved (every reason resolves to ReasonUnknown at the
// metrics boundary, NOT to a stringly-typed value that could leak
// into the Prometheus label).
func NewNoopTranslationReasonClassifier() TranslationReasonClassifier {
	return &noopTranslationReasonClassifier{}
}

// translatorFuncAdapter is the canonical adapter type for
// `translation.TranslatorFunc` → `ScriptTranslator`. The adapter
// holds the function by value; the Translate method delegates.
// The adapter's own nil-value satisfies the ScriptTranslator
// interface (the Translate method is nil-safe at the adapter
// level, so an unwired adapter behaves as a no-op translator).
type translatorFuncAdapter func(ctx context.Context, text, targetLanguage string) (string, error)

// Translate satisfies ScriptTranslator. Nil-receiver returns
// ("", nil) — the postprocessor's nil-port check fires FIRST (so
// the adapter is never invoked nil), but the method itself is
// nil-safe for defense-in-depth.
func (f translatorFuncAdapter) Translate(ctx context.Context, text, targetLanguage string) (string, error) {
	if f == nil {
		return "", nil
	}
	return f(ctx, text, targetLanguage)
}

// noopTranslationUseCase is the canonical nil-tolerance fallback
// for the TranslationUseCase port. The zero-value struct satisfies
// the port with a sentinel error response so the postprocessor's
// typed-error classification surfaces the canonical "no-op"
// reason at the metrics boundary.
//
// The sentinel error is intentionally a free-form message (NOT a
// typed sentinel from usecase/) so the noop fallback doesn't
// import the usecase package — keeping the noop boundary pure
// (ports/ is the SOLE canonical owner; usecase/ is forbidden).
type noopTranslationUseCase struct{}

// TranslateScriptSpec on a noop fallback returns the canonical
// "noop translation use case" sentinel. The postprocessor's
// classifyFunc maps the .Error() string to a bounded
// TranslationWarningReason at the metrics boundary.
func (*noopTranslationUseCase) TranslateScriptSpec(
	ctx context.Context,
	in *scriptpkg.ModelScriptOutputV1,
	evidence *scriptpkg.ClipEvidence,
	targetLang string,
	translator ScriptTranslator,
) (out *scriptpkg.ModelScriptOutputV1, warnings []string, err error) {
	return nil, nil, errNoopTranslationUseCase
}

// noopTranslationReasonClassifier is the canonical nil-tolerance
// fallback for the TranslationReasonClassifier port. The zero-value
// struct satisfies the port with ReasonUnknown coercion so the
// bounded-reason contract is preserved under all composition
// scenarios.
//
// godlike/07 NO-FAKE-AVAILABILITY: this is the canonical
// fail-closed surface for the classifier port — the bounded-enum
// guarantee is enforced at the noop layer, not just at the
// real-adapter layer. A future agent that wires a real classifier
// MUST preserve the ReasonUnknown coercion for any value not
// matching the canonical 10-token set.
type noopTranslationReasonClassifier struct{}

// ClassifyReason on a noop fallback always returns ReasonUnknown.
// godlike/07 NO-FAKE-AVAILABILITY: even a nil-receiver noop is
// safe — the method does NOT touch receiver state, so the bounded
// cardinality is guaranteed under all composition scenarios.
func (*noopTranslationReasonClassifier) ClassifyReason(s string) TranslationWarningReason {
	return ReasonUnknown
}

// errNoopTranslationUseCase is the sentinel returned by the noop
// TranslationUseCase implementation. The postprocessor's
// classifyFunc maps the .Error() string to ReasonTranslatorMissing
// (the canonical bounded reason) at the metrics boundary.
//
// godlike/07 typed-error contract: the string is a FREE-FORM
// message (not a typed sentinel from usecase/) to preserve the
// ports/ ↔ usecase/ layering boundary. The postprocessor's
// ClassifyReason in usecase/ substring-matches on "noop translation
// use case" (and falls through to ReasonUnknown for unmatched
// values), so the message text is the diagnostic seam that
// surfaces the noop fallback to operator dashboards.
var errNoopTranslationUseCase = &noopTranslationUseCaseError{}

type noopTranslationUseCaseError struct{}

func (e *noopTranslationUseCaseError) Error() string {
	return "translate script: noop translation use case (composition gap; check Wire script post-processors)"
}
