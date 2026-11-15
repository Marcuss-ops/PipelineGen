package scriptgeneration

import (
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// projectEditorialCandidates is the candidate-set counterpart of
// projectEditorialOverlays. It applies the same provenance, language,
// grounding and timing checks but returns every valid title, bullet and
// opening surface instead of choosing one bullet per scene. The single-call
// Rust compiler owns the choice; the legacy selector stays untouched until
// that call is wired.
func projectEditorialCandidates(result *GenerateResult, language Language, scenes []Scene, resolved []ResolvedScene, inputs []capabilityoverlay.SceneInput, globalCategories map[scriptpkg.OverlayCategory]scriptpkg.Toggle, covered ...map[string]bool) []capabilityoverlay.SceneInput {
	coveredScenes := map[string]bool{}
	if len(covered) > 0 && covered[0] != nil {
		coveredScenes = covered[0]
	}
	shortPhraseScenes := map[string]bool{}
	if len(covered) > 1 && covered[1] != nil {
		shortPhraseScenes = covered[1]
	}
	if result == nil || result.Editorial == nil || !result.Editorial.Certified ||
		result.Editorial.SchemaVersion != scriptpkg.EditorialManifestVersion ||
		len(strings.TrimSpace(result.Editorial.Fingerprint)) != 64 ||
		strings.Trim(strings.TrimSpace(result.Editorial.Fingerprint), "0123456789abcdef") != "" ||
		!strings.EqualFold(strings.TrimSpace(result.Editorial.ProfileVersion), "highlights.v1") ||
		!strings.EqualFold(strings.TrimSpace(result.Editorial.SourceLanguage), string(language)) ||
		(strings.TrimSpace(string(result.SourceLanguage)) != "" && !strings.EqualFold(strings.TrimSpace(string(result.SourceLanguage)), string(language))) ||
		result.Editorial.Validate() != nil {
		return inputs
	}
	byInput := make(map[string]int, len(inputs))
	for i := range inputs {
		byInput[inputs[i].ID] = i
	}
	byScene := make(map[string]Scene, len(scenes))
	for _, scene := range scenes {
		byScene[scene.ID] = scene
	}
	bounds := make(map[string][2]int64, len(resolved))
	for _, scene := range resolved {
		if scene.TimelineStartUS >= 0 && scene.DurationUS > 0 {
			bounds[scene.ID] = [2]int64{scene.TimelineStartUS, scene.TimelineStartUS + scene.DurationUS}
		}
	}
	for _, editorial := range result.Editorial.Scenes {
		scene, exists := byScene[editorial.SceneID]
		window, hasBounds := bounds[editorial.SceneID]
		if !exists || !hasBounds || scene.ExecutionMode.IsFixedMedia() || editorial.SceneID != strings.TrimSpace(editorial.SceneID) {
			continue
		}
		if shortPhraseScenes[editorial.SceneID] {
			continue
		}
		text := strings.TrimSpace(scene.Text[language])
		if text == "" {
			continue
		}
		inputIndex, hasInput := byInput[editorial.SceneID]
		if !hasInput {
			inputs = append(inputs, capabilityoverlay.SceneInput{ID: editorial.SceneID})
			inputIndex = len(inputs) - 1
			byInput[editorial.SceneID] = inputIndex
		}
		voiceover, ok := scene.Voiceover[language]
		if !ok || voiceover.Timing == nil {
			continue
		}
		if _, err := capabilityaudio.LocatePhrase(*voiceover.Timing, text); err != nil {
			continue
		}
		titleCategoryEnabled := !editorialCategoryDisabled(scene, scriptpkg.OverlayCategoryTitles, globalCategories)
		bulletCategoryEnabled := !editorialCategoryDisabled(scene, scriptpkg.OverlayCategoryBullets, globalCategories)
		titleAllowed := titleCategoryEnabled && editorial.TitleStatus == "resolved" && editorial.Title != nil &&
			validEditorialTitleSource(editorial.TitleSource) && editorialTitleIsGrounded(text, strings.TrimSpace(*editorial.Title))
		if titleAllowed {
			if located, ok := locateEditorialPhrase(*voiceover.Timing, text, strings.TrimSpace(*editorial.Title)); ok &&
				located.StartUS >= 0 && located.EndUS > located.StartUS && located.EndUS <= voiceover.Timing.DurationUS {
				annotation := editorialTimedAnnotation(located, window[0], "scene_title", 1.0)
				annotation.EditorialCandidate = "title"
				inputs[inputIndex].Phrases = append(inputs[inputIndex].Phrases, annotation)
			}
		}
		if coveredScenes[editorial.SceneID] {
			continue
		}
		if bulletCategoryEnabled {
			for _, bullet := range editorial.Bullets {
				if bullet.SentenceStart < 0 || bullet.SentenceEnd <= bullet.SentenceStart {
					continue
				}
				longText := editorialBulletLongPhrase(bullet)
				if longText == "" {
					continue
				}
				if located, ok := locateEditorialPhrase(*voiceover.Timing, text, longText); ok &&
					located.StartUS >= 0 && located.EndUS > located.StartUS && located.EndUS <= voiceover.Timing.DurationUS {
					annotation := editorialTimedAnnotation(located, window[0], "scene_bullet", 0.99)
					annotation.EditorialCandidate = "bullet"
					inputs[inputIndex].Phrases = append(inputs[inputIndex].Phrases, annotation)
				}
			}
		}
		if bulletCategoryEnabled && !hasOpeningSceneBullet(inputs[inputIndex].Phrases) && editorial.Title == nil && editorial.TitleStatus == "unavailable" {
			if opening, ok := editorialOpeningPhrase(inputs[inputIndex], text, *voiceover.Timing); ok {
				opening.PresentationRole = "scene_bullet"
				opening.EditorialCandidate = "opening"
				opening.StartUS = window[0]
				opening.StartMs = window[0] / 1000
				opening.DurationUS = min(int64(3_000_000), window[1]-window[0])
				opening.EndMs = (opening.StartUS + opening.DurationUS + 999) / 1000
				inputs[inputIndex].Phrases = append(inputs[inputIndex].Phrases, opening)
			}
		}
		for i := range inputs[inputIndex].Phrases {
			p := &inputs[inputIndex].Phrases[i]
			if p.PresentationRole != "scene_title" && p.PresentationRole != "scene_bullet" {
				continue
			}
			start, end := p.StartUS, p.StartUS+p.DurationUS
			if start < window[0] || end > window[1] || end <= start {
				p.PresentationRole = ""
			}
		}
	}
	return inputs
}
