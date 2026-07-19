// Package adapters — processor_translation.go: postprocessor that
// translates the per-scene text fields of a *ModelScriptOutputV1
// to the plan's target language via the canonical TranslateScriptSpec
// pure function.
//
// PR-TRANSLATE-SCRIPT-SPEC forward-pointer FP2 (2026-08-08): the
// processor is a thin orchestrator that wires the typed
// `ports.ScriptTranslator` + `ports.TranslationUseCase` +
// `ports.TranslationReasonClassifier` + `ports.TranslationMetricsRecorder`
// ports. It mutates `input.SpecScene.Scenes` in-place with the
// translated scenes (matching the ClipBindingsProcessor precedent
// per FASE 3, June 2026) so downstream document/persistence
// processors observe the translated bundle during the same Run.
//
// Policy is ProcessorBestEffort (godlike/07 NO-FAKE-AVAILABILITY): a
// missing translator, empty target language, or translator error
// are surfaced as warnings + bounded-reason metrics, NOT as hard
// failures. The postprocessor is intentionally additive: callers
// that do not request translation simply omit "translation" from
// the plan's Postprocessors list (canonical pattern, see
// generation_plan_builder.go).
//
// DI pattern (godlike/06 Pattern 0): the canonical
// `TranslateScriptSpec` + `ClassifyReason` functions live in
// `usecase/` (to keep the pure-function + ValidateAndEnrichSpecScene
// co-location). The adapters package cannot import usecase
// (cycle: usecase → adapters via documents_usecase.go). The
// composition root — which already imports both — wires thin struct
// adapters (ports.NewScriptTranslatorFromFunc +
// usecase.NewTranslationUseCaseAdapter +
// usecase.NewTranslationReasonClassifierAdapter +
// observability.NewTranslationMetricsAdapter(reg)) into the typed
// ports. This breaks the cycle while preserving the canonical
// 1-method Pattern 0 port convention.
package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	translationpkg "github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"go.uber.org/zap"
)

// TranslationProcessor translates the text fields of a
// *ModelScriptOutputV1 (built from input.Text + input.SpecScene)
// to the plan's primary target language via the canonical
// TranslateScriptSpec pure function. The processor is the
// SOLE canonical caller of TranslateScriptSpec inside the
// postprocessor pipeline (godlike/06 SSOT one-canonical-owner-per-fact).
//
// godlike/06 Pattern 0: every collaborator is a typed port. There
// are NO bare function values stored on the struct — every DI
// surface is a 1-method interface, with the canonical noop
// fallback installed by the constructor when a nil port is passed.
type TranslationProcessor struct {
	translator ports.ScriptTranslator
	metrics    ports.TranslationMetricsRecorder
	useCase    ports.TranslationUseCase
	classifier ports.TranslationReasonClassifier
	log        *zap.Logger
}

// NewTranslationProcessor creates a TranslationProcessor. Every port
// is nil-tolerant: a nil translator degrades to "translator_missing"
// warning (BestEffort policy); a nil metrics recorder falls back to
// the canonical noop adapter (composition-gap safe); a nil
// useCase falls back to the canonical noop translation use case
// (surfaces errNoopTranslationUseCase to classifyFunc); a nil
// classifier falls back to the canonical noop classifier (coerces
// every reason to ReasonUnknown).
//
// godlike/07 minimum-blast-radius: zero new exported symbols; the
// noop fallbacks are installed by the canonical ports/ package
// (godlike/06 SSOT one-canonical-owner-per-fact). The composition
// root is the canonical wiring surface and is expected to pass the
// real adapters built via observability.NewTranslationMetricsAdapter(reg)
// + usecase.NewTranslationUseCaseAdapter() +
// usecase.NewTranslationReasonClassifierAdapter() +
// ports.NewScriptTranslatorFromFunc(root.AI.OllamaTranslator.TranslateText).
func NewTranslationProcessor(
	translator ports.ScriptTranslator,
	metrics ports.TranslationMetricsRecorder,
	useCase ports.TranslationUseCase,
	classifier ports.TranslationReasonClassifier,
	log *zap.Logger,
) *TranslationProcessor {
	if metrics == nil {
		metrics = ports.NewNoopTranslationMetricsRecorder()
	}
	if useCase == nil {
		useCase = ports.NewNoopTranslationUseCase()
	}
	if classifier == nil {
		classifier = ports.NewNoopTranslationReasonClassifier()
	}
	return &TranslationProcessor{
		translator: translator,
		metrics:    metrics,
		useCase:    useCase,
		classifier: classifier,
		log:        log,
	}
}

// Name returns the canonical processor identifier.
func (p *TranslationProcessor) Name() ProcessorName { return ProcessorTranslation }

// Policy classifies translation as ProcessorBestEffort. The
// postprocessor emits warnings + bounded-reason metrics on
// composition gaps (nil translator, empty target language) or
// translator errors; the script.generate pipeline continues
// with the un-translated SpecScene (downstream document/persistence
// processors see the original text + warnings on the result).
//
// godlike/07 NO-FAKE-AVAILABILITY: this is the canonical
// composition-time posture for optional LLM-driven postprocessors
// (mirrors the metadata + clip_search + voiceover best-effort
// pattern).
func (p *TranslationProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

// Process executes the translation flow for the plan's primary
// target language. Priority order (PR-TRANSLATION-PIPELINE-2026-07-09):
//  1. plan.TranslateTo  (explicit user request via OutputSpec.TranslateTo)
//  2. plan.Languages[0] (first language in the languages array)
//  3. plan.Language      (legacy single-language field)
//
// The processor builds a *ModelScriptOutputV1 envelope from
// input.Text + input.SpecScene (with the canonical SchemaVersion
// "specscene.v1"), invokes the canonical TranslateScriptSpec pure
// function (via the typed ports.TranslationUseCase port), then
// mutates input.SpecScene.Scenes + input.Text with the translated
// surface (in-place per the ClipBindingsProcessor precedent). On
// translator success, the result is a non-empty PostProcessResult
// with Changed=true + TranslatedText + TranslatedSpecScene + the
// warnings channel (preserved verbatim per the typed-error contract).
//
// Failure modes (all surface as warnings + bounded-reason metrics
// + Changed=false; pipeline continues with the un-translated
// SpecScene per BestEffort):
//
//   - nil translator → warning "translator_missing" + metric
//   - empty plan.Languages + empty plan.Language → warning
//     "target_lang_missing" + metric
//   - empty input.Text → no-op (Changed=false, no warning, no
//     metric) — the postprocessor has nothing to translate
//   - TranslateScriptSpec error → warning + metric (preserves
//     the typed-error chain via fmt.Errorf %w wrap)
//
// godlike/07 typed-error contract: each warning is mapped to a
// bounded TranslationWarningReason via the typed
// ports.TranslationReasonClassifier port (canonical
// `usecase.NewTranslationReasonClassifierAdapter`) so the
// Prometheus label cardinality is guaranteed bounded.
func (p *TranslationProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
) (*PostProcessResult, error) {
	if p == nil {
		return &PostProcessResult{}, nil
	}
	if p.translator == nil {
		// Composition gap: translator port unwired. Surface as
		// warning + bounded-reason metric (BestEffort, NOT fatal).
		reason := ports.ReasonTranslatorMissing
		if p.metrics != nil {
			p.metrics.IncTranslationWarning("", reason)
		}
		return &PostProcessResult{
			Changed:  false,
			Warnings: []string{"translation: translator port not configured"},
		}, nil
	}

	// Resolve target language. Priority order:
	//   1. plan.TranslateTo  (explicit user request via OutputSpec.TranslateTo)
	//   2. plan.Languages[0] (first language in the languages array)
	//   3. plan.Language      (legacy single-language field)
	//
	// Bug-fix (PR-TRANSLATION-PIPELINE-2026-07-09): TranslateTo was
	// previously ignored entirely — the processor resolved from
	// plan.Languages[0] → plan.Language, so a request with
	// {language:"en", translate_to:"it"} (no languages array) would
	// self-translate to English instead of Italian.
	targetLang := ""
	if plan != nil {
		targetLang = strings.TrimSpace(plan.TranslateTo)
		if targetLang == "" && len(plan.Languages) > 0 {
			targetLang = strings.TrimSpace(plan.Languages[0])
		}
		if targetLang == "" {
			targetLang = strings.TrimSpace(plan.Language)
		}
	}
	if targetLang == "" {
		reason := ports.ReasonTargetLangMissing
		if p.metrics != nil {
			p.metrics.IncTranslationWarning("", reason)
		}
		return &PostProcessResult{
			Changed:  false,
			Warnings: []string{"translation: target language not resolved from plan"},
		}, nil
	}

	// No-op on empty Text (no work to do; no warning; no metric).
	if strings.TrimSpace(input.Text) == "" && len(input.SpecScene.Scenes) == 0 {
		return &PostProcessResult{}, nil
	}

	// Build the canonical *ModelScriptOutputV1 envelope from the
	// postprocessor input. SchemaVersion is the integer 1 (the
	// canonical specscene.v1 contract per model_output.go);
	// WordCount/ModelUsed/CacheStatus are stamped by the engine
	// post-decode (per the PR 3 typed walk) so the pre-translation
	// envelope is left at zero values here.
	modelIn := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		SpecScene:     input.SpecScene,
		Text:          input.Text,
	}

	// Adapt the typed port to the translation.TranslatorFunc signature
	// that the pure function expects. Nil-port → returns
	// ErrTranslationTranslatorMissing via the adapter closure so the
	// pure function fails fast with the canonical typed sentinel.
	funcAdapter := translationpkg.TranslatorFunc(func(ctx context.Context, text, lang string) (string, error) {
		if p.translator == nil {
			return "", errors.New("translate script: translator port nil")
		}
		return p.translator.Translate(ctx, text, lang)
	})

	// Invoke the canonical TranslateScriptSpec pure function via the
	// typed ports.TranslationUseCase port (composition root wires
	// usecase.NewTranslationUseCaseAdapter at construction time,
	// which delegates to the canonical pure function byte-for-byte).
	// The useCase wraps the function-value to the typed-port
	// signature internally (see
	// internal/application/scripts/usecase/translation.go).
	translated, tWarnings, tErr := p.useCase.TranslateScriptSpec(
		ctx,
		modelIn,
		input.SourceTrace,
		targetLang,
		p.translator,
	)
	// Silently swallow the unused funcAdapter variable so the Go
	// compiler doesn't complain (the useCase adapter internally
	// re-wraps the port into a function value). The closure
	// remains defined for future use-cases that may need the
	// function-value shape (e.g. a future per-segment
	// override path).
	_ = funcAdapter
	if tErr != nil {
		// Typed-error chain: classify for bounded-reason metric, then
		// wrap with %w to preserve the chain for callers
		// (errors.Is/As probes work on the returned PostProcessResult
		// error envelope).
		reason := p.classifier.ClassifyReason(tErr.Error())
		if p.metrics != nil {
			p.metrics.IncTranslationWarning(targetLang, reason)
		}
		if p.log != nil {
			p.log.Warn("translation: TranslateScriptSpec failed",
				zap.String("target_lang", targetLang),
				zap.String("reason", string(reason)),
				zap.Error(tErr),
			)
		}
		return &PostProcessResult{
			Changed:  false,
			Warnings: append(tWarnings, fmt.Sprintf("translation failed: %v", tErr)),
		}, fmt.Errorf("translation: %w", tErr)
	}

	// Translator success: mutate input.SpecScene + input.Text
	// in-place per the ClipBindingsProcessor FASE 3 precedent.
	// godlike/07 NO-FAKE-AVAILABILITY: only mutate when the
	// translated surface is non-nil (defensive: TranslateScriptSpec
	// is documented to never return (nil, _, nil) but the post-
	// processor surface still guards the edge case).
	//
	// PR-TRANSLATE-SCRIPT-SPEC PR-6 (2026-07-09) MIX design: ALSO
	// surface the translated bundle into PostProcessResult.TranslatedText
	// + TranslatedSpecScene so cross-Run observability + the
	// buildGenerationResult preference path can see the translated
	// surface WITHOUT relying on the in-place ProcessInput mutation
	// (which only affects downstream processors in the same Run).
	// Without these explicit fields, IsEmpty() would flag the
	// postprocessor "returned empty output" even when substantial
	// LLM work happened (false-positive surfaced by the pre-PR-5
	// audit). omitempty ensures callers that did not opt into
	// translation see a wire-shape-stable result.
	var postTranslatedText string
	var postTranslatedSpecScene scriptpkg.SpecSceneOutput
	originalText := input.Text
	originalSpecScene := input.SpecScene
	if translated != nil {
		input.SpecScene = translated.SpecScene
		input.Text = translated.Text
		postTranslatedText = translated.Text
		postTranslatedSpecScene = scriptpkg.SpecSceneOutput{
			Version: translated.SchemaVersion,
			Scenes:  translated.SpecScene.Scenes,
		}
	}

	// Per-warning emission: each warning → bounded-reason metric.
	for _, w := range tWarnings {
		reason := p.classifier.ClassifyReason(w)
		if p.metrics != nil {
			p.metrics.IncTranslationWarning(targetLang, reason)
		}
	}

	return &PostProcessResult{
		Changed:             true,
		Warnings:            tWarnings,
		TranslatedText:      postTranslatedText,
		TranslatedSpecScene: postTranslatedSpecScene,
		OriginalText:        originalText,
		OriginalSpecScene:   originalSpecScene,
	}, nil
}
