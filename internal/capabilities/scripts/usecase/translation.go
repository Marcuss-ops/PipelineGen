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

	translationpkg "github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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

// TranslateScriptSpec is the canonical LLM-based script translation
// surface for the script.generate pipeline. It translates the text fields of a *ModelScriptOutputV1
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
		// PRE-EXISTING-7 (FASE 13): use the ENRICHED baseline
		// (scene.Text) rather than the raw inputScene.Text. The
		// pre-translation ValidateAndEnrichSpecScene populates
		// canonical semantic markers (Pacquiao/Broner/Round N in
		// the canonical 8-scene Pacquiao-Broner fixture) on scene;
		// translating scene.Text ensures the markers reach the LLM
		// and survive translation across all 4 languages.
		textResult, textEqualSource, textErr := translateTextSegment(
			ctx, translator, scene.Text, targetLang,
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
		// PRE-EXISTING-7 (FASE 13): use the enriched baseline
		// (scene.Title) to keep title text in lockstep with body text.
		titleResult, titleEqualSource, titleErr := translateTextSegment(
			ctx, translator, scene.Title, targetLang,
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
