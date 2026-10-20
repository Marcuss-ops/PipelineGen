package scriptgeneration

import (
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// chapterBulletWindowUS is the on-screen duration of a chapter-bullet card.
// The bullet is visual-only (never spoken), so it does not borrow word-level
// TTS timing: it opens at its scene window start — linked to the first
// phrase — and holds for a fixed readable beat clamped to the scene window.
const chapterBulletWindowUS = int64(4_000_000)

// chapterBulletScore ranks below the editorial title (1.0) and editorial
// bullet (0.99) so a scene carrying both keeps the editorial title on top
// while the chapter bullet wins the single bullet slot (see covered set).
const chapterBulletScore = 0.98

// ChapterBulletRole aliases the overlays wire value for chapter cards.
// It rides the bullets overlay category like scene_bullet, but the planner
// gives it its own motion family (text_3d) and the style finalizer its own
// font bump, so a chapter opening reads as a chapter on screen.
const ChapterBulletRole = capabilityoverlay.ChapterBulletRole

// projectChapterBullets maps ChapterManifest bullets onto scene overlay
// inputs as visual-only chapter_bullet cards. Every rule is fail-closed:
//
//   - the bullet text must ground verbatim (case-insensitive) in exactly
//     the owning body scene's narration — never invented, never borrowed
//     from another scene;
//   - one bullet per scene, each manifest bullet rendered at most once;
//   - fixed-media scenes are skipped (their text is display-only, never
//     BODY narration the chapters were extracted from);
//   - no voiceover/TTS is required: timing comes from the scene's canonical
//     timeline window, anchored at the first phrase when one exists;
//   - language must match the result source language the chapters were
//     extracted from.
//
// It returns the augmented inputs and the set of covered scene IDs. Covered
// scenes keep their editorial title but must skip the editorial bullet so
// the one-bullet-per-scene visual contract holds.
func projectChapterBullets(result *GenerateResult, language Language, scenes []Scene, resolved []ResolvedScene, inputs []capabilityoverlay.SceneInput, globalCategories ...map[scriptpkg.OverlayCategory]scriptpkg.Toggle) ([]capabilityoverlay.SceneInput, map[string]bool) {
	covered := map[string]bool{}
	if result == nil || result.ChapterManifest == nil ||
		result.ChapterManifest.SchemaVersion != "chapter_manifest.v1" {
		return inputs, covered
	}
	if strings.TrimSpace(string(result.SourceLanguage)) != "" &&
		!strings.EqualFold(strings.TrimSpace(string(result.SourceLanguage)), string(language)) {
		return inputs, covered
	}
	byInput := make(map[string]int, len(inputs))
	for i := range inputs {
		byInput[inputs[i].ID] = i
	}
	bounds := make(map[string][2]int64, len(resolved))
	for _, scene := range resolved {
		if scene.TimelineStartUS >= 0 && scene.DurationUS > 0 {
			bounds[scene.ID] = [2]int64{scene.TimelineStartUS, scene.TimelineStartUS + scene.DurationUS}
		}
	}
	// Sentence offsets map the transcript-global bullet spans onto scenes.
	// The transcript is the ordered join of body scenes, so offsets are
	// cumulative Go-splitter sentence counts over the same body scenes.
	// Splitter drift vs Rust is tolerated: offsets only nominate a scene,
	// verbatim text containment always confirms it (fail closed).
	offsets := make(map[string][2]int, len(scenes))
	cursor := 0
	for _, scene := range scenes {
		if scene.ExecutionMode.IsFixedMedia() || !scene.ExecutionMode.CountsTowardBodyWordBudget() {
			continue
		}
		count := len(editorialSentences(strings.TrimSpace(scene.Text[language])))
		offsets[scene.ID] = [2]int{cursor, cursor + count}
		cursor += count
	}
	var ordered []scriptpkg.ChapterBullet
	for _, chapter := range result.ChapterManifest.Chapters {
		for _, bullet := range chapter.Bullets {
			if strings.TrimSpace(bullet.Text) == "" {
				continue
			}
			ordered = append(ordered, bullet)
		}
	}
	// Dedup by normalized text: two manifest entries quoting the same span
	// must not produce two identical on-screen cards in different scenes.
	used := make(map[string]struct{}, len(ordered))
	global := map[scriptpkg.OverlayCategory]scriptpkg.Toggle{}
	if len(globalCategories) > 0 && globalCategories[0] != nil {
		global = globalCategories[0]
	}
	for _, scene := range scenes {
		if covered[scene.ID] {
			continue
		}
		// Respect an explicit category opt-out before adding a chapter card.
		// The planner has one phrase slot per scene; inserting a bullet and
		// filtering it later can otherwise evict the ordinary grounded phrase
		// that should occupy that slot when bullets are disabled.
		if editorialCategoryDisabled(scene, scriptpkg.OverlayCategoryBullets, global) {
			continue
		}
		// An explicitly designated short phrase owns this scene's text lane.
		// Projecting the same sentence as a chapter bullet would cause
		// `bullets: disabled` to remove the requested phrase overlay.
		if scene.ShortPhraseOverlay {
			continue
		}
		if scene.ExecutionMode.IsFixedMedia() || !scene.ExecutionMode.CountsTowardBodyWordBudget() {
			continue
		}
		text := strings.TrimSpace(scene.Text[language])
		if text == "" {
			continue
		}
		window, hasBounds := bounds[scene.ID]
		if !hasBounds || window[1] <= window[0] {
			continue
		}
		sceneRange, hasRange := offsets[scene.ID]
		for _, bullet := range ordered {
			bulletText := strings.TrimSpace(bullet.Text)
			if _, taken := used[strings.ToLower(bulletText)]; taken {
				continue
			}
			if !chapterBulletHasSubstance(bulletText) {
				continue
			}
			if !chapterBulletGroundsInScene(bullet, bulletText, text, sceneRange, hasRange) {
				continue
			}
			inputIndex, hasInput := byInput[scene.ID]
			if !hasInput {
				inputs = append(inputs, capabilityoverlay.SceneInput{ID: scene.ID})
				inputIndex = len(inputs) - 1
				byInput[scene.ID] = inputIndex
			}
			startUS, durationUS := chapterBulletWindow(inputs[inputIndex], window)
			if durationUS <= 0 {
				continue
			}
			inputs[inputIndex].Phrases = append(inputs[inputIndex].Phrases,
				capabilityoverlay.TimedAnnotation{
					Text: bulletText, StartMs: startUS / 1000, EndMs: (startUS + durationUS + 999) / 1000,
					StartUS: startUS, DurationUS: durationUS, Score: chapterBulletScore,
					PresentationRole: ChapterBulletRole,
				})
			used[strings.ToLower(bulletText)] = struct{}{}
			covered[scene.ID] = true
			break // the visual contract is one bullet per scene
		}
	}
	return inputs, covered
}

// chapterBulletWindow anchors the card at the scene window start, linked to
// the first phrase when one opens the scene. The card never overflows the
// scene window and never has a non-positive duration (fail closed).
func chapterBulletWindow(input capabilityoverlay.SceneInput, window [2]int64) (int64, int64) {
	startUS := window[0]
	for _, phrase := range input.Phrases {
		if phrase.DurationUS <= 0 || phrase.StartUS < window[0] || phrase.StartUS >= window[1] {
			continue
		}
		startUS = phrase.StartUS
		break // linked to the first phrase, then stop
	}
	durationUS := min(chapterBulletWindowUS, window[1]-startUS)
	if durationUS <= 0 {
		return 0, 0
	}
	return startUS, durationUS
}

// chapterBulletGroundsInScene decides whether a transcript-global bullet may
// render on a scene. Full verbatim containment wins directly. Otherwise the
// bullet may still open its chapter on the first overlapping scene: the span
// must overlap the scene's sentence range (nominating, tolerant to splitter
// drift) AND the bullet lead (first words) must ground verbatim in the scene
// (confirming). A merged multi-sentence bullet therefore renders once, at its
// chapter's opening scene, instead of never rendering at all.
func chapterBulletGroundsInScene(bullet scriptpkg.ChapterBullet, bulletText, sceneText string, sceneRange [2]int, hasRange bool) bool {
	if strings.Contains(strings.ToLower(sceneText), strings.ToLower(bulletText)) {
		return true
	}
	if !hasRange || sceneRange[1] <= sceneRange[0] {
		return false
	}
	overlaps := bullet.StartSentence < sceneRange[1] && bullet.EndSentence > sceneRange[0]
	if !overlaps {
		return false
	}
	lead := chapterBulletLead(bulletText, 5)
	return lead != "" && strings.Contains(strings.ToLower(sceneText), strings.ToLower(lead))
}

// chapterBulletLead returns the first n words of a bullet for grounding: words
// are splitter-independent, so boundary drift between the Rust and Go sentence
// splitters cannot break the confirmation.
func chapterBulletLead(text string, n int) string {
	words := strings.Fields(strings.TrimSpace(text))
	if len(words) == 0 {
		return ""
	}
	if len(words) > n {
		words = words[:n]
	}
	return strings.Join(words, " ")
}

// chapterBulletHasSubstance mirrors the Rust extractor floor (MIN_BULLET_WORDS
// / MIN_BULLET_CHARS): a two-word tail is a fragment, not a point. Scripts
// without spaces are measured by alphanumeric runes so CJK stays eligible.
func chapterBulletHasSubstance(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	if len(strings.Fields(trimmed)) >= 4 {
		return true
	}
	runes := 0
	for _, r := range trimmed {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 127 {
			runes++
		}
	}
	return runes >= 16
}
