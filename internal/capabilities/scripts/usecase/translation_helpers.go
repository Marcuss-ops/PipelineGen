package usecase

import (
	"context"
	"fmt"
	"strings"

	translationpkg "github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

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
