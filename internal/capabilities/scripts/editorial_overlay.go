package scriptgeneration

import (
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// projectEditorialOverlays adds only extractive editorial text whose manifest,
// language, scene identity, verbatim grounding and word-level audio timing all
// agree. Every failure is a no-op: narration/annotations remain the fallback.
func projectEditorialOverlays(result *GenerateResult, language Language, scenes []Scene, resolved []ResolvedScene, inputs []capabilityoverlay.SceneInput) []capabilityoverlay.SceneInput {
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
	transcript := result.Output.Text
	if strings.TrimSpace(transcript) == "" {
		transcript = strings.Join(sceneTextsInOrder(scenes, language), "\n\n")
	}
	for _, editorial := range result.Editorial.Scenes {
		scene, exists := byScene[editorial.SceneID]
		window, hasBounds := bounds[editorial.SceneID]
		if !exists || !hasBounds || scene.ExecutionMode.IsFixedMedia() || editorial.SceneID != strings.TrimSpace(editorial.SceneID) {
			continue
		}
		text := strings.TrimSpace(scene.Text[language])
		if text == "" || !validEditorialManifestScene(editorial, text, transcript) {
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
		// The timing is content-addressed to a narration. Prove that its
		// complete word sequence still matches this exact scene text before
		// borrowing any local phrase offsets; a title substring alone is not
		// enough to rule out stale timing from a previous translation.
		if _, err := capabilityaudio.LocatePhrase(*voiceover.Timing, text); err != nil {
			continue
		}
		if editorial.TitleStatus == "resolved" && editorial.Title != nil &&
			(!validEditorialTitleSource(editorial.TitleSource) || !editorialTitleIsGrounded(text, strings.TrimSpace(*editorial.Title))) {
			continue // an unknown source or ungrounded title invalidates the editorial scene
		}
		bulletInvalid := false
		for _, bullet := range editorial.Bullets {
			if !validEditorialBullet(editorial, bullet, text, transcript) {
				bulletInvalid = true
				break
			}
		}
		if bulletInvalid {
			continue // reject all editorial text from an internally inconsistent scene
		}
		if editorial.TitleStatus == "resolved" && editorial.Title != nil {
			if located, ok := locateEditorialPhrase(*voiceover.Timing, text, strings.TrimSpace(*editorial.Title)); ok &&
				located.StartUS >= 0 && located.EndUS > located.StartUS && located.EndUS <= voiceover.Timing.DurationUS {
				inputs[inputIndex].Phrases = append(inputs[inputIndex].Phrases,
					editorialTimedAnnotation(located, window[0], "scene_title", 1.0))
			}
		}
		for _, bullet := range editorial.Bullets {
			if located, ok := locateEditorialPhrase(*voiceover.Timing, text, strings.TrimSpace(bullet.Text)); ok &&
				located.StartUS >= 0 && located.EndUS > located.StartUS && located.EndUS <= voiceover.Timing.DurationUS {
				inputs[inputIndex].Phrases = append(inputs[inputIndex].Phrases,
					editorialTimedAnnotation(located, window[0], "scene_bullet", 0.99))
				break // the visual contract is one bullet per scene
			}
		}
		for i := range inputs[inputIndex].Phrases {
			p := &inputs[inputIndex].Phrases[i]
			if p.PresentationRole != "scene_title" && p.PresentationRole != "scene_bullet" {
				continue
			}
			start, end := p.StartUS, p.StartUS+p.DurationUS
			if start < window[0] || end > window[1] || end <= start {
				p.PresentationRole = "" // canonical scene/audio bounds fail closed
			}
		}
	}
	return inputs
}

func validEditorialManifestScene(scene scriptpkg.EditorialScene, text, transcript string) bool {
	if strings.TrimSpace(scene.SceneID) == "" || scene.TitleStatus != "resolved" && scene.TitleStatus != "unavailable" {
		return false
	}
	if scene.TitleStatus == "unavailable" && scene.Title != nil {
		return false
	}
	if scene.TitleStatus == "resolved" && (scene.Title == nil || strings.TrimSpace(*scene.Title) == "") {
		return false
	}
	if scene.Title != nil && !editorialTitleIsGrounded(text, strings.TrimSpace(*scene.Title)) {
		return false
	}
	if scene.TitleStatus == "resolved" && !validEditorialTitleSource(scene.TitleSource) {
		return false
	}
	for _, bullet := range scene.Bullets {
		if !validEditorialBullet(scene, bullet, text, transcript) {
			return false
		}
	}
	for _, highlight := range scene.Highlights {
		raw := []byte(transcript)
		if highlight.StartByte < 0 || highlight.EndByte <= highlight.StartByte || highlight.EndByte > len(raw) ||
			!highlight.VisualEligible || strings.TrimSpace(highlight.Text) == "" ||
			string(raw[highlight.StartByte:highlight.EndByte]) != highlight.Text ||
			!strings.Contains(strings.ToLower(text), strings.ToLower(highlight.Text)) {
			return false
		}
	}
	return true
}

func editorialTitleIsGrounded(sceneText, title string) bool {
	return title != "" && strings.Contains(strings.ToLower(sceneText), strings.ToLower(title))
}

func validEditorialTitleSource(source string) bool {
	switch strings.TrimSpace(source) {
	case "topic_validated", "extractive_keyphrase":
		return true
	default:
		return false
	}
}

func validEditorialBullet(scene scriptpkg.EditorialScene, bullet scriptpkg.EditorialSceneBullet, text, transcript string) bool {
	candidate := strings.TrimSpace(bullet.Text)
	if candidate == "" || bullet.SentenceStart < 0 || bullet.SentenceEnd <= bullet.SentenceStart ||
		!strings.Contains(strings.ToLower(text), strings.ToLower(candidate)) {
		return false
	}
	source := text
	if scene.GloballyIndexed {
		source = transcript
	}
	sentences := editorialSentences(source)
	if bullet.SentenceEnd > len(sentences) {
		return false
	}
	span := strings.TrimSpace(strings.Join(sentences[bullet.SentenceStart:bullet.SentenceEnd], " "))
	return strings.EqualFold(span, candidate)
}

func sceneTextsInOrder(scenes []Scene, language Language) []string {
	texts := make([]string, 0, len(scenes))
	for _, scene := range scenes {
		if scene.ExecutionMode.CountsTowardBodyWordBudget() {
			if text := strings.TrimSpace(scene.Text[language]); text != "" {
				texts = append(texts, text)
			}
		}
	}
	return texts
}

func editorialSentences(text string) []string {
	var out []string
	start := 0
	for index, r := range text {
		if r != '.' && r != '!' && r != '?' && r != '\n' {
			continue
		}
		end := index + len(string(r))
		if sentence := strings.TrimSpace(text[start:end]); sentence != "" {
			out = append(out, sentence)
		}
		start = end
	}
	if sentence := strings.TrimSpace(text[start:]); sentence != "" {
		out = append(out, sentence)
	}
	return out
}

func locateEditorialPhrase(timing capabilityaudio.SpeechTimingArtifact, sceneText, phrase string) (capabilityaudio.LocatedPhrase, bool) {
	if strings.TrimSpace(sceneText) == "" || strings.TrimSpace(phrase) == "" ||
		!strings.Contains(strings.ToLower(sceneText), strings.ToLower(strings.TrimSpace(phrase))) {
		return capabilityaudio.LocatedPhrase{}, false
	}
	located, err := capabilityaudio.LocatePhrase(timing, phrase)
	if err != nil || len(located) == 0 {
		return capabilityaudio.LocatedPhrase{}, false
	}
	return located[0], true
}

func editorialTimedAnnotation(span capabilityaudio.LocatedPhrase, timelineStartUS int64, role string, score float64) capabilityoverlay.TimedAnnotation {
	startUS := timelineStartUS + span.StartUS
	endUS := timelineStartUS + span.EndUS
	return capabilityoverlay.TimedAnnotation{
		Text: span.Text, StartMs: startUS / 1000, EndMs: (endUS + 999) / 1000,
		StartUS: startUS, DurationUS: endUS - startUS, Score: score, PresentationRole: role,
	}
}
