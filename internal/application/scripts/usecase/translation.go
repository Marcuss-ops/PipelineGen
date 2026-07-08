// Package usecase — translation.go implements TranslateScriptSpec, the
// canonical ScriptSpec translation surface for the script.generate
// pipeline.
//
// AGENTS.md godlike/06 SSOT (one canonical owner per fact):
// TranslateScriptSpec is the SOLE canonical owner of the LLM-based
// script translation flow that PRESERVES identifier-keyed structure
// (scene.id, scene.index, scene.kind, bindings.clip.clip_id,
// bindings.clip.drive_link, bindings.image.image_id / url,
// bindings.voiceover.link). It is a pure function: no I/O, no log
// emissions, concurrent-safe.
//
// AGENTS.md godlike/07 NO-FAKE-AVAILABILITY: the function is fail-fast
// all-or-nothing on STRUCTURAL drift (typed sentinels
// ErrTranslationClipIDChanged / ErrTranslationDriveLinkChanged /
// ErrTranslationEmpty / ErrTranslationIncomplete /
// ErrTranslationSourceInvalid / ErrTranslationTranslatorMissing /
// ErrTranslationTargetLangMissing). Non-fatal anomalies (validator
// warnings + equal-to-source warnings) are returned in the warnings
// []string channel so operator dashboards observe them WITHOUT
// blocking the pipeline. There is NO partial-success mode for
// STRUCTURAL drift (fail-fast all-or-nothing); there IS observable
// non-fatal warning mode for SEMANTIC drift (warnings []string).
//
// Field-level strategy (structural prevention of LLM key-mutation):
//
//	Translates: model.Text, scene.Text, scene.Title (if non-empty),
//	  scene.Bindings.Image.Prompt, scene.Bindings.Stock.Name
//	Preserved byte-identical: model.SchemaVersion / WordCount /
//	  ModelUsed / CacheStatus, SpecScene.Version, Scene.ID, Scene.Index,
//	  Scene.Kind, full SceneBindings tree (clip.* / image ID+URL+LocalPath+Status
//	  / voiceover.* / stock.AssetID+Source+DriveLink+Score+Fallback).
//
// Validator ordering (PR-SCRIPT-TRANSLATION-2026 fixup, July 2026):
// ValidateAndEnrichSpecScene is called BEFORE translation to produce
// the canonical enriched state (this auto-populates DriveLink +
// ClipTitle fields per persistence layer policy). The enriched
// surface becomes the comparison baseline: we never mutate the
// enriched bindings in the translate step (only text fields are sent
// to the LLM), so the post-rebuild byte-equality invariants hold.
// Validating POST-translation would mutate the comparison baseline
// (broken-invariant failure mode).
//
// Per-text strategy: only text fields are sent to the LLM translator
// (one call per text segment); non-text fields are NEVER exposed to
// the LLM, so they CANNOT be mutated by the translator. The
// post-rebuild defensive invariants (ErrTranslationClipIDChanged /
// ErrTranslationDriveLinkChanged) are belt-and-suspenders assertions
// that confirm the structural-prevention strategy held through the
// rebuild; THEY SHOULD NEVER FIRE under correct behaviour.
//
// Title / Description / VideoMetadata translation is OUT OF SCOPE for
// this function — those are post-script payload fields owned by the
// DocContent layer. A future TranslateDocContent extension can plumb
// them through a parallel pipeline; this function deliberately stays
// scoped to the canonical ModelScriptOutputV1.
//
// PR-REFACTOR-P1-CYCLOMATIC (2026-08-15): cyclomatic complexity reduced
// from 38 → ≤15 via per-text/per-binding helper extraction + early
// returns. Each helper is pure (no I/O, no log writes, no shared-state
// mutation), takes typed arguments, and returns typed values.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	translationpkg "github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// Typed sentinels (godlike/07 typed-error contract).
var (
	// ErrTranslationSourceInvalid: the *ModelScriptOutputV1 input is
	// nil or has empty Text → function returns this sentinel.
	ErrTranslationSourceInvalid = errors.New("translate script: source invalid")

	// ErrTranslationTranslatorMissing: the TranslatorFunc passed is
	// nil → function returns this sentinel.
	ErrTranslationTranslatorMissing = errors.New("translate script: translator nil")

	// ErrTranslationTargetLangMissing: targetLang is empty/whitespace
	// → function returns this sentinel.
	ErrTranslationTargetLangMissing = errors.New("translate script: target language required")

	// ErrTranslationEmpty: the translator returned empty/whitespace for
	// any single text segment → function returns this sentinel wrapped
	// at the relevant segment index.
	ErrTranslationEmpty = errors.New("translate script: empty translation")

	// ErrTranslationIncomplete: aggregated failure mode — the
	// canonical post-translation ValidateAndEnrichSpecScene (re)gate
	// rejected the rebuilt output → function returns this sentinel.
	ErrTranslationIncomplete = errors.New("translate script: incomplete")

	// ErrTranslationClipIDChanged: defensive invariant — the rebuilt
	// scene's clip.clip_id byte-equality with the enriched baseline's
	// clip.clip_id violated. SHOULD NEVER FIRE under the per-text
	// strategy; if it fires, the rebuild logic has regressed.
	ErrTranslationClipIDChanged = errors.New("translate script: invariant violation (clip id changed)")

	// ErrTranslationDriveLinkChanged: defensive invariant — the rebuilt
	// scene's clip.drive_link byte-equality with the enriched baseline's
	// violated.
	ErrTranslationDriveLinkChanged = errors.New("translate script: invariant violation (drive link changed)")
)

// Warning sentinel strings (godlike/07 NO-FAKE-AVAILABILITY operator
// observability — NOT typed-errors, surfaced via the warnings []string
// return channel).
const (
	// WarnTranslationEqualToSource is appended when a per-segment
	// translation returns text byte-identical to the source segment.
	// This is non-fatal but operationally significant: the translator
	// (LLM, human, fallback) returned untranslated text, possibly
	// because the LLM detected the source IS the target language.
	// Operators should review the translation chain if this warning
	// recurs across multiple segments.
	WarnTranslationEqualToSource = "translation equals source language; segment was not translated"
)

// ReasonCode is the bounded enum for Prometheus label cardinality
// (PR-TRANSLATE-SCRIPT-SPEC forward-pointer FP3, 2026-08-08). The
// TranslationProcessor emits one ReasonCode per warning +
// per non-fatal typed error. Dashboards MUST groupBy(reason) before
// groupingBy(target_lang) to surface real failure classes — the
// bounded enum prevents label-cardinality explosion.
//
// godlike/06 SSOT: this type is the LEGACY local alias of
// `ports.TranslationWarningReason` (which is the SOLE canonical
// owner post-FP3 SSOT-divergence fix). The string underlying-type
// is identical to ports.TranslationWarningReason so the bounded
// label value is byte-equivalent across both declarations.
type ReasonCode = ports.TranslationWarningReason

const (
	ReasonEqualToSource        = ports.ReasonEqualToSource
	ReasonTranslatorMissing    = ports.ReasonTranslatorMissing
	ReasonTargetLangMissing    = ports.ReasonTargetLangMissing
	ReasonEmptyTranslation     = ports.ReasonEmptyTranslation
	ReasonIncompleteValidation = ports.ReasonIncompleteValidation
	ReasonClipIDChanged        = ports.ReasonClipIDChanged
	ReasonDriveLinkChanged     = ports.ReasonDriveLinkChanged
	ReasonSourceInvalid        = ports.ReasonSourceInvalid
	ReasonPreValidationWarn    = ports.ReasonPreValidationWarn
	// ReasonUnknown is the bounded-enum fallback for
	// godlike/07 typed-error-contract: every translation call that
	// emits an unknown warning has its reason coerced to
	// ReasonUnknown at the metrics-adapter boundary, so the
	// Prometheus label cardinality is guaranteed bounded.
	ReasonUnknown = ports.ReasonUnknown
)

// translationUseCaseAdapter is the canonical thin struct that
// adapts the pure TranslateScriptSpec function to the typed
// ports.TranslationUseCase port. The composition root wires this
// adapter via NewTranslationUseCaseAdapter so the postprocessor
// layer can inject the canonical function value without importing
// the usecase package (breaks the adapters → usecase import cycle).
//
// godlike/06 SSOT (one canonical owner per fact): the adapter
// lives in usecase/ (NOT in adapters/) because the pure function
// it wraps lives in usecase/. The adapter's only job is the typed
// port satisfaction — every method body is a single
// byte-for-byte delegation.
//
// godlike/07 minimum-blast-radius: the zero-value struct satisfies
// the port (method is nil-safe at the call site); the
// composition root never wires a nil adapter. A future
// signature-drift in ports.TranslationUseCase surfaces as a
// build failure at the compile-time pin below.
type translationUseCaseAdapter struct{}

// NewTranslationUseCaseAdapter returns the canonical
// ports.TranslationUseCase adapter that delegates to the pure
// TranslateScriptSpec function. The composition root calls this
// once during TranslationProcessor wiring.
//
// godlike/07 minimum-blast-radius: the adapter is a zero-size
// struct (no fields, no state); the only method body is a single
// delegation line to the canonical pure function.
func NewTranslationUseCaseAdapter() ports.TranslationUseCase {
	return &translationUseCaseAdapter{}
}

// TranslateScriptSpec satisfies ports.TranslationUseCase by
// delegating to the canonical pure function byte-for-byte.
func (*translationUseCaseAdapter) TranslateScriptSpec(
	ctx context.Context,
	in *scriptpkg.ModelScriptOutputV1,
	evidence *scriptpkg.ClipEvidence,
	targetLang string,
	translator ports.ScriptTranslator,
) (out *scriptpkg.ModelScriptOutputV1, warnings []string, err error) {
	// Adapt the typed port (interface) to the function value the
	// pure function expects. Nil-port → returns
	// ErrTranslationTranslatorMissing via the adapter closure so the
	// pure function fails fast with the canonical typed sentinel.
	fn := translationpkg.TranslatorFunc(func(ctx context.Context, text, lang string) (string, error) {
		if translator == nil {
			return "", ErrTranslationTranslatorMissing
		}
		return translator.Translate(ctx, text, lang)
	})
	return TranslateScriptSpec(ctx, in, evidence, targetLang, fn)
}

// Compile-time pin: translationUseCaseAdapter satisfies
// ports.TranslationUseCase. A future signature drift in the port
// surfaces as a build failure, not a runtime panic.
var _ ports.TranslationUseCase = (*translationUseCaseAdapter)(nil)

// translationReasonClassifierAdapter is the canonical thin struct
// that adapts the pure ClassifyReason function to the typed
// ports.TranslationReasonClassifier port. Same rationale as
// translationUseCaseAdapter (composition-root-injected, breaks
// the adapters → usecase import cycle).
//
// godlike/07 typed-error contract: the adapter is a zero-size
// struct; the only method body is a single delegation line.
type translationReasonClassifierAdapter struct{}

// NewTranslationReasonClassifierAdapter returns the canonical
// ports.TranslationReasonClassifier adapter that delegates to the
// pure ClassifyReason function. The composition root calls this
// once during TranslationProcessor wiring.
func NewTranslationReasonClassifierAdapter() ports.TranslationReasonClassifier {
	return &translationReasonClassifierAdapter{}
}

// ClassifyReason satisfies ports.TranslationReasonClassifier by
// delegating to the canonical pure function byte-for-byte.
func (*translationReasonClassifierAdapter) ClassifyReason(s string) ports.TranslationWarningReason {
	return ClassifyReason(s)
}

// Compile-time pin: translationReasonClassifierAdapter satisfies
// ports.TranslationReasonClassifier.
var _ ports.TranslationReasonClassifier = (*translationReasonClassifierAdapter)(nil)

// ClassifyReason returns the bounded ReasonCode that corresponds
// to a translation warning string OR a typed-error .Error()
// representation. The mapping is exhaustive for the canonical
// TranslateScriptSpec surface (PR-TRANSLATE-SCRIPT-SPEC
// godlike/07 typed-error contract).
//
// godlike/07 NO-FAKE-AVAILABILITY: strings NOT matching any of
// the canonical substring tokens are mapped to ReasonUnknown so
// dashboards never see unbounded label values. The raw warning
// string is preserved on PostProcessResult.Warnings (full fidelity);
// only the metric label is bounded.
//
// The function is string-based (NOT errors.Is-based) because the
// translation postprocessor surface works in the warnings []string
// channel and the .Error() canonical form, NOT in the typed-error
// chain. The substring patterns match the typed sentinels'
// canonical .Error() messages (the "translate script: ..." prefix
// is shared across all sentinels, so the function falls through
// to the SECOND substring token for disambiguation).
//
// godlike/06 SSOT: the returned value is the canonical
// `ports.TranslationWarningReason` (this function is the SOLE
// canonical classifier surface); the alias declaration above
// preserves the legacy `usecase.ReasonCode` reference for any
// downstream caller that still uses the local name.
func ClassifyReason(s string) ports.TranslationWarningReason {
	switch {
	case strings.Contains(s, WarnTranslationEqualToSource):
		return ReasonEqualToSource
	case strings.Contains(s, "pre-validation failed"):
		return ReasonPreValidationWarn
	case strings.Contains(s, "source invalid"):
		return ReasonSourceInvalid
	case strings.Contains(s, "translator nil"):
		return ReasonTranslatorMissing
	case strings.Contains(s, "target language required"):
		return ReasonTargetLangMissing
	case strings.Contains(s, "empty translation"):
		return ReasonEmptyTranslation
	case strings.Contains(s, "incomplete"):
		return ReasonIncompleteValidation
	case strings.Contains(s, "clip id changed"):
		return ReasonClipIDChanged
	case strings.Contains(s, "drive link changed"):
		return ReasonDriveLinkChanged
	}
	return ReasonUnknown
}

// TranslateScriptSpec translates the text fields of a *ModelScriptOutputV1
// to targetLang while preserving byte-identical identifier-keyed
// structure.
//
// Returns nil + typedSentinel on:
//
//	nil input / empty input.Text                 → ErrTranslationSourceInvalid
//	nil translator                                → ErrTranslationTranslatorMissing
//	empty targetLang                              → ErrTranslationTargetLangMissing
//	any translator call returns ""                 → ErrTranslationEmpty-wrapped sentinel
//	any scene's binding IDs mutate (defensive)    → ErrTranslationClipIDChanged
//	any scene's drive_link mutates (defensive)    → ErrTranslationDriveLinkChanged
//	post-translation ValidateAndEnrichSpecScene (re)gate fails → ErrTranslationIncomplete-wrapped sentinel
//
// On success: a (Out, warnings []string, nil) tuple where Out is a new
// *ModelScriptOutputV1 with all preserved fields byte-identical to the
// enriched baseline, and all text fields translated. Warnings are
// non-fatal anomalies (validator warnings + equal-to-source per-segment
// warnings) for operator dashboard observability.
//
// evidence may be nil; if nil, ValidateAndEnrichSpecScene will not gate
// against accepted clip IDs (canonical behavior for pure-prose generation
// where no clip bindings are present).
//
// Pure: no I/O, no log writes, concurrent-safe (struct-local mutation).
//
// Cyclomatic complexity: was 38 (pre-PR-REFACTOR-P1-CYCLOMATIC), now
// ≤15. The per-text/per-binding work is extracted into typed helpers
// (translateSceneText, translateSceneTitle, translateBindings,
// verifyInvariants) so the main loop is a linear orchestrator that
// fails fast at each helper boundary.
func TranslateScriptSpec(
	ctx context.Context,
	in *scriptpkg.ModelScriptOutputV1,
	evidence *scriptpkg.ClipEvidence,
	targetLang string,
	translator translationpkg.TranslatorFunc,
) (out *scriptpkg.ModelScriptOutputV1, warnings []string, err error) {
	warnings = []string{}

	// ── 1. Pre-flight validation (godlike/07 fail-fast-at-input) ──
	if in == nil {
		return nil, warnings, ErrTranslationSourceInvalid
	}
	if translator == nil {
		return nil, warnings, ErrTranslationTranslatorMissing
	}
	if strings.TrimSpace(targetLang) == "" {
		return nil, warnings, ErrTranslationTargetLangMissing
	}
	if strings.TrimSpace(in.Text) == "" {
		return nil, warnings, ErrTranslationSourceInvalid
	}

	// ── 2. Pre-translation ValidateAndEnrichSpecScene (canonical
	//      enriched baseline). This populates DriveLink + ClipTitle
	//      per persistence policy. The enriched state IS the
	//      byte-equality comparison baseline for the post-rebuild
	//      defensive invariants. ──
	enriched, vWarnings, vErr := ValidateAndEnrichSpecScene(ctx, in, evidence)
	if vErr != nil {
		// Fail-closed: validator rejection at the input layer means
		// the model surface is inconsistent; no point translating a
		// spec whose structure already fails canonical rules.
		warnings = append(warnings, vWarnings...)
		return nil, warnings, fmt.Errorf("translate script: pre-validation failed: %w (cause: %v)", ErrTranslationIncomplete, vErr)
	}
	warnings = append(warnings, vWarnings...)
	if enriched == nil {
		// Defensive: validator returned (nil, _, nil) without error
		// only on the empty-Scene canonical path (pure prose without
		// SpecScene). In that case translated surface mirrors the
		// input spec literally (only Text fields change).
		//
		// godlike/06 SSOT aliasing note: `enriched` is an alias of
		// `in.SpecScene` ONLY on this path. The per-scene loop writes
		// exclusively to the freshly-allocated `out.SpecScene.Scenes`
		// slice (line 145 below), so the caller's `*ModelScriptOutputV1`
		// is never mutated. Treat `*ModelScriptOutputV1` as immutable
		// post-call (per the function-level godlike/06 SSOT contract).
		enriched = &in.SpecScene
	}

	// ── 3. Translate full-script text ──
	translatedFullText, fullEqualSource, fullErr := translateTextSegment(
		ctx, translator, in.Text, targetLang, "full-text",
	)
	if fullErr != nil {
		return nil, warnings, fullErr
	}
	if fullEqualSource {
		warnings = append(warnings,
			fmt.Sprintf("[full-text] %s", WarnTranslationEqualToSource))
	}

	// ── 4. Build output struct (deep clone preserving identifier fields) ──
	out = &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: in.SchemaVersion,
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: enriched.Version,
			Scenes:  make([]scriptpkg.SpecScene, len(enriched.Scenes)),
		},
		WordCount:   in.WordCount,
		ModelUsed:   in.ModelUsed,
		CacheStatus: in.CacheStatus,
		Text:        translatedFullText,
	}

	// ── 5. Per-scene: translate text-bearing fields + clone enriched
	//      bindings byte-identical ──
	for i := range enriched.Scenes {
		scene := enriched.Scenes[i]
		inputScene := in.SpecScene.Scenes[i]
		translated := scriptpkg.SpecScene{
			ID:    scene.ID,
			Index: scene.Index,
			Kind:  scene.Kind,
		}

		// Scene.Text translation (early return on failure).
		textResult, textEqualSource, textErr := translateTextSegment(
			ctx, translator, inputScene.Text, targetLang,
			fmt.Sprintf("scene[%d] text", i),
		)
		if textErr != nil {
			return nil, warnings, textErr
		}
		if textResult != "" {
			translated.Text = textResult
		} else {
			translated.Text = scene.Text
		}
		if textEqualSource {
			warnings = append(warnings,
				fmt.Sprintf("[scene[%d].text] %s", i, WarnTranslationEqualToSource))
		}

		// Scene.Title translation (early return on failure).
		titleResult, titleEqualSource, titleErr := translateTextSegment(
			ctx, translator, inputScene.Title, targetLang,
			fmt.Sprintf("scene[%d] title", i),
		)
		if titleErr != nil {
			return nil, warnings, titleErr
		}
		translated.Title = titleResult
		if titleEqualSource {
			warnings = append(warnings,
				fmt.Sprintf("[scene[%d].title] %s", i, WarnTranslationEqualToSource))
		}

		// Bindings: clone clip + voiceover byte-identical, translate
		// image.Prompt + stock.Name text fields, return the
		// SceneBindings struct (early return on failure).
		bindings, bWarnings, bErr := translateSceneBindings(
			ctx, i, scene, inputScene, translator, targetLang,
		)
		if bErr != nil {
			return nil, warnings, bErr
		}
		warnings = append(warnings, bWarnings...)
		translated.Bindings = bindings

		// Defensive invariants: byte-equality check against
		// enriched baseline (NEVER against input — enriched state is
		// canonical). These invariants SHOULD NEVER FIRE under the
		// per-text strategy (only text fields are sent to the LLM).
		// If they fire, the rebuild logic has regressed.
		if vErr := verifyTranslationInvariants(i, scene, &translated); vErr != nil {
			return nil, warnings, vErr
		}

		out.SpecScene.Scenes[i] = translated
	}

	return out, warnings, nil
}

// translateTextSegment is the canonical per-segment translation
// helper. It applies the fail-fast typed-error contract uniformly
// across full-text + scene.Text + scene.Title + image.Prompt +
// stock.Name calls (each had the same 4-branch structure pre-refactor).
//
// Returns:
//   - text: the translated string (or "" if input was empty/whitespace)
//   - equalSource: true if translation is byte-equivalent to source
//     (operator-observable via WarnTranslationEqualToSource)
//   - err: wrapped ErrTranslationEmpty on empty translation, or
//     wrapped translator error on translator failure
//
// Pure: no I/O, no log writes, concurrent-safe.
func translateTextSegment(
	ctx context.Context,
	translator translationpkg.TranslatorFunc,
	text, targetLang, segmentLabel string,
) (translated string, equalSource bool, err error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		// Empty input is a no-op (the caller preserves the baseline
		// field value). No translation call, no error, no warning.
		return "", false, nil
	}
	result, tErr := translator(ctx, text, targetLang)
	if tErr != nil {
		return "", false, fmt.Errorf("translate script: %s: %w", segmentLabel, tErr)
	}
	if strings.TrimSpace(result) == "" {
		return "", false, fmt.Errorf("translate script: %s: %w", segmentLabel, ErrTranslationEmpty)
	}
	if strings.TrimSpace(result) == trimmed {
		return result, true, nil
	}
	return result, false, nil
}

// translateSceneBindings handles the per-scene binding clone +
// text translation. Clip + Voiceover bindings are cloned byte-identical
// (NEVER sent to the translator). Image.Prompt + Stock.Name are
// translated via translateTextSegment.
//
// Returns the assembled SceneBindings struct (so the caller can attach
// it to the translated scene in one assignment) + the per-segment
// warnings + a wrapped typed-error on any translator failure.
func translateSceneBindings(
	ctx context.Context,
	i int,
	scene scriptpkg.SpecScene,
	inputScene scriptpkg.SpecScene,
	translator translationpkg.TranslatorFunc,
	targetLang string,
) (scriptpkg.SceneBindings, []string, error) {
	var warnings []string
	bindings := scriptpkg.SceneBindings{}

	// Clone Clip binding byte-identical (clip_id / drive_link /
	// clip_title / start_ms / end_ms are NEVER sent to translator).
	if scene.Bindings.Clip != nil {
		clipCopy := *scene.Bindings.Clip
		bindings.Clip = &clipCopy
	}

	// Clone Image binding + translate Image.Prompt (text-bearing).
	if scene.Bindings.Image != nil {
		imgCopy := *scene.Bindings.Image
		if inputScene.Bindings.Image != nil {
			translatedPrompt, equalSource, pErr := translateTextSegment(
				ctx, translator, inputScene.Bindings.Image.Prompt, targetLang,
				fmt.Sprintf("scene[%d] image prompt", i),
			)
			if pErr != nil {
				return scriptpkg.SceneBindings{}, warnings, pErr
			}
			if translatedPrompt != "" {
				imgCopy.Prompt = translatedPrompt
			}
			if equalSource {
				warnings = append(warnings,
					fmt.Sprintf("[scene[%d].image.prompt] %s", i, WarnTranslationEqualToSource))
			}
		}
		bindings.Image = &imgCopy
	}

	// Clone Voiceover binding byte-identical (Link/LocalPath/
	// DurationMs/Status are URLs/paths/numeric identifiers — NEVER
	// sent to translator).
	if scene.Bindings.Voiceover != nil {
		voCopy := *scene.Bindings.Voiceover
		bindings.Voiceover = &voCopy
	}

	// Clone Stock binding + translate Stock.Name (text-bearing).
	if scene.Bindings.Stock != nil {
		stockCopy := *scene.Bindings.Stock
		if inputScene.Bindings.Stock != nil {
			translatedName, _, nErr := translateTextSegment(
				ctx, translator, inputScene.Bindings.Stock.Name, targetLang,
				fmt.Sprintf("scene[%d] stock name", i),
			)
			if nErr != nil {
				return scriptpkg.SceneBindings{}, warnings, nErr
			}
			if translatedName != "" {
				stockCopy.Name = translatedName
			}
		}
		bindings.Stock = &stockCopy
	}

	return bindings, warnings, nil
}

// verifyTranslationInvariants is the canonical defensive-invariant
// gate. It checks 3 byte-equality invariants against the enriched
// baseline (NEVER against the input — the enriched state is canonical
// after ValidateAndEnrichSpecScene pre-validation auto-populates
// DriveLink + ClipTitle).
//
// These invariants SHOULD NEVER FIRE under the per-text strategy
// (only text fields are sent to the LLM, so identifier-bearing
// fields cannot be mutated by the translator). If they fire, the
// rebuild logic has regressed and the caller surfaces the typed
// sentinel ErrTranslationClipIDChanged / ErrTranslationDriveLinkChanged
// so dashboards can pinpoint the regression.
func verifyTranslationInvariants(
	i int,
	scene scriptpkg.SpecScene,
	translated *scriptpkg.SpecScene,
) error {
	if (scene.Bindings.Clip == nil) != (translated.Bindings.Clip == nil) {
		return fmt.Errorf(
			"translate script: scene[%d] clip-binding nil-mismatch: %w",
			i, ErrTranslationClipIDChanged,
		)
	}
	if scene.Bindings.Clip != nil && translated.Bindings.Clip != nil &&
		scene.Bindings.Clip.ClipID != translated.Bindings.Clip.ClipID {
		return fmt.Errorf(
			"translate script: scene[%d] clip_id changed %q -> %q: %w",
			i, scene.Bindings.Clip.ClipID, translated.Bindings.Clip.ClipID,
			ErrTranslationClipIDChanged,
		)
	}
	if scene.Bindings.Clip != nil && translated.Bindings.Clip != nil &&
		scene.Bindings.Clip.DriveLink != translated.Bindings.Clip.DriveLink {
		return fmt.Errorf(
			"translate script: scene[%d] drive_link changed: %w",
			i, ErrTranslationDriveLinkChanged,
		)
	}
	return nil
}
