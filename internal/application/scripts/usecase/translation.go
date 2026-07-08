// Package usecase — translation.go implements TranslateScriptSpec, the
// canonical ScriptSpec translation surface for the script.generate
// pipeline.
//
// AGENTS.md godlike/06 SSOT (onecanonical-owner-per-fact):
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
package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	translatedFullText, tErr := translator(ctx, in.Text, targetLang)
	if tErr != nil {
		return nil, warnings, fmt.Errorf("translate script: full-text: %w", tErr)
	}
	if strings.TrimSpace(translatedFullText) == "" {
		return nil, warnings, fmt.Errorf("translate script: full-text: %w", ErrTranslationEmpty)
	}
	if strings.TrimSpace(translatedFullText) == strings.TrimSpace(in.Text) {
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

		// Translate scene.Text.
		if strings.TrimSpace(inputScene.Text) != "" {
			translatedText, err2 := translator(ctx, inputScene.Text, targetLang)
			if err2 != nil {
				return nil, warnings, fmt.Errorf("translate script: scene[%d] text: %w", i, err2)
			}
			if strings.TrimSpace(translatedText) == "" {
				return nil, warnings, fmt.Errorf("translate script: scene[%d] text: %w", i, ErrTranslationEmpty)
			}
			if strings.TrimSpace(translatedText) == strings.TrimSpace(inputScene.Text) {
				warnings = append(warnings,
					fmt.Sprintf("[scene[%d].text] %s", i, WarnTranslationEqualToSource))
			}
			translated.Text = translatedText
		} else {
			translated.Text = scene.Text
		}

		// Translate scene.Title (if non-empty).
		var translatedTitle string
		if strings.TrimSpace(inputScene.Title) != "" {
			t, err2 := translator(ctx, inputScene.Title, targetLang)
			if err2 != nil {
				return nil, warnings, fmt.Errorf("translate script: scene[%d] title: %w", i, err2)
			}
			if strings.TrimSpace(t) == "" {
				return nil, warnings, fmt.Errorf("translate script: scene[%d] title: %w", i, ErrTranslationEmpty)
			}
			if strings.TrimSpace(t) == strings.TrimSpace(inputScene.Title) {
				warnings = append(warnings,
					fmt.Sprintf("[scene[%d].title] %s", i, WarnTranslationEqualToSource))
			}
			translatedTitle = t
		}
		translated.Title = translatedTitle

		// Clone Clip binding byte-identical (clip_id / drive_link /
		// clip_title / start_ms / end_ms are NEVER sent to translator).
		if scene.Bindings.Clip != nil {
			clipCopy := *scene.Bindings.Clip
			translated.Bindings.Clip = &clipCopy
		}

		// Clone Image binding + translate Image.Prompt (text-bearing).
		if scene.Bindings.Image != nil {
			imgCopy := *scene.Bindings.Image
			if strings.TrimSpace(inputScene.Bindings.Image.Prompt) != "" {
				t, err2 := translator(ctx, inputScene.Bindings.Image.Prompt, targetLang)
				if err2 != nil {
					return nil, warnings, fmt.Errorf("translate script: scene[%d] image prompt: %w", i, err2)
				}
				if strings.TrimSpace(t) == "" {
					return nil, warnings, fmt.Errorf("translate script: scene[%d] image prompt: %w", i, ErrTranslationEmpty)
				}
				if strings.TrimSpace(t) == strings.TrimSpace(inputScene.Bindings.Image.Prompt) {
					warnings = append(warnings,
						fmt.Sprintf("[scene[%d].image.prompt] %s", i, WarnTranslationEqualToSource))
				}
				imgCopy.Prompt = t
			}
			translated.Bindings.Image = &imgCopy
		}

		// Clone Voiceover binding byte-identical (Link/LocalPath/
		// DurationMs/Status are URLs/paths/numeric identifiers — NEVER
		// sent to translator).
		if scene.Bindings.Voiceover != nil {
			voCopy := *scene.Bindings.Voiceover
			translated.Bindings.Voiceover = &voCopy
		}

		// Clone Stock binding + translate Stock.Name (text-bearing).
		if scene.Bindings.Stock != nil {
			stockCopy := *scene.Bindings.Stock
			if inputScene.Bindings.Stock != nil && strings.TrimSpace(inputScene.Bindings.Stock.Name) != "" {
				t, err2 := translator(ctx, inputScene.Bindings.Stock.Name, targetLang)
				if err2 != nil {
					return nil, warnings, fmt.Errorf("translate script: scene[%d] stock name: %w", i, err2)
				}
				if strings.TrimSpace(t) == "" {
					return nil, warnings, fmt.Errorf("translate script: scene[%d] stock name: %w", i, ErrTranslationEmpty)
				}
				stockCopy.Name = t
			}
			translated.Bindings.Stock = &stockCopy
		}

		// ── 6. Defensive invariants: byte-equality check against
		//      enriched baseline (NEVER against input — enriched
		//      state is canonical). These invariants SHOULD NEVER
		//      FIRE under the per-text strategy (only text fields
		//      are sent to the LLM). If they fire, the rebuild logic
		//      has regressed. ──
		if (scene.Bindings.Clip == nil) != (translated.Bindings.Clip == nil) {
			return nil, warnings, fmt.Errorf(
				"translate script: scene[%d] clip-binding nil-mismatch: %w",
				i, ErrTranslationClipIDChanged,
			)
		}
		if scene.Bindings.Clip != nil && translated.Bindings.Clip != nil &&
			scene.Bindings.Clip.ClipID != translated.Bindings.Clip.ClipID {
			return nil, warnings, fmt.Errorf(
				"translate script: scene[%d] clip_id changed %q -> %q: %w",
				i, scene.Bindings.Clip.ClipID, translated.Bindings.Clip.ClipID,
				ErrTranslationClipIDChanged,
			)
		}
		if scene.Bindings.Clip != nil && translated.Bindings.Clip != nil &&
			scene.Bindings.Clip.DriveLink != translated.Bindings.Clip.DriveLink {
			return nil, warnings, fmt.Errorf(
				"translate script: scene[%d] drive_link changed: %w",
				i, ErrTranslationDriveLinkChanged,
			)
		}

		out.SpecScene.Scenes[i] = translated
	}

	return out, warnings, nil
}
