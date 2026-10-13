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
		// The timing is content-addressed to a narration. Prove that its
		// complete word sequence still matches this exact scene text before
		// borrowing any local phrase offsets; a title substring alone is not
		// enough to rule out stale timing from a previous translation.
		if _, err := capabilityaudio.LocatePhrase(*voiceover.Timing, text); err != nil {
			continue
		}
		titleAllowed := editorial.TitleStatus == "resolved" && editorial.Title != nil &&
			validEditorialTitleSource(editorial.TitleSource) && editorialTitleIsGrounded(text, strings.TrimSpace(*editorial.Title))
		// Editorial.Validate above certifies the manifest structure. Ground
		// every displayed title or bullet phrase independently against this
		// scene's narration and exact word timing below; a paraphrased summary
		// must not suppress a valid, extractive opening phrase.
		if titleAllowed {
			if located, ok := locateEditorialPhrase(*voiceover.Timing, text, strings.TrimSpace(*editorial.Title)); ok &&
				located.StartUS >= 0 && located.EndUS > located.StartUS && located.EndUS <= voiceover.Timing.DurationUS {
				inputs[inputIndex].Phrases = append(inputs[inputIndex].Phrases,
					editorialTimedAnnotation(located, window[0], "scene_title", 1.0))
			}
		}
		for _, bullet := range editorial.Bullets {
			shortText := editorialBulletShortPhrase(editorial, bullet)
			if shortText == "" {
				continue
			}
			if located, ok := locateEditorialPhrase(*voiceover.Timing, text, shortText); ok &&
				located.StartUS >= 0 && located.EndUS > located.StartUS && located.EndUS <= voiceover.Timing.DurationUS {
				annotation := editorialTimedAnnotation(located, window[0], "scene_bullet", 0.99)
				// Show the extractive bullet as the narration scene opens. Its
				// source phrase remains voiceover-timed for grounding, while its
				// display window gives the short-phrase motion a readable hold.
				annotation.StartUS = window[0]
				annotation.StartMs = window[0] / 1000
				annotation.DurationUS = min(int64(3_000_000), window[1]-window[0])
				annotation.EndMs = (annotation.StartUS + annotation.DurationUS + 999) / 1000
				inputs[inputIndex].Phrases = append(inputs[inputIndex].Phrases, annotation)
				break // the visual contract is one bullet per scene
			}
		}
		if !hasOpeningSceneBullet(inputs[inputIndex].Phrases) {
			if opening, ok := editorialOpeningPhrase(inputs[inputIndex], text, *voiceover.Timing); ok {
				opening.PresentationRole = "scene_bullet"
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
				p.PresentationRole = "" // canonical scene/audio bounds fail closed
			}
		}
	}
	return inputs
}

func hasOpeningSceneBullet(phrases []capabilityoverlay.TimedAnnotation) bool {
	for _, phrase := range phrases {
		if phrase.PresentationRole == "scene_bullet" {
			return true
		}
	}
	return false
}

func editorialOpeningPhrase(input capabilityoverlay.SceneInput, sceneText string, timing capabilityaudio.SpeechTimingArtifact) (capabilityoverlay.TimedAnnotation, bool) {
	candidates := make([]capabilityoverlay.TimedAnnotation, 0, len(input.Phrases)+len(input.Keywords)+len(input.Quotes)+len(input.Numbers))
	candidates = append(candidates, input.Phrases...)
	candidates = append(candidates, input.Keywords...)
	candidates = append(candidates, input.Quotes...)
	candidates = append(candidates, input.Numbers...)
	best := capabilityoverlay.TimedAnnotation{}
	found := false
	for _, candidate := range candidates {
		words := strings.Fields(strings.TrimSpace(candidate.Text))
		if len(words) < 2 || candidate.DurationUS <= 0 || candidate.EndMs <= candidate.StartMs {
			continue
		}
		if len(words) > 5 {
			words = words[:5]
		}
		phrase := strings.Join(words, " ")
		located, ok := locateEditorialPhrase(timing, sceneText, phrase)
		if !ok || located.EndUS <= located.StartUS || located.EndUS > timing.DurationUS {
			continue
		}
		if !found || candidate.Score > best.Score {
			best = capabilityoverlay.TimedAnnotation{
				Text: located.Text, StartUS: located.StartUS, StartMs: located.StartUS / 1000,
				EndMs: (located.EndUS + 999) / 1000, DurationUS: located.EndUS - located.StartUS,
				Score: candidate.Score,
			}
			found = true
		}
	}
	if found {
		return best, true
	}
	// Final exact-source fallback: take at most five words from the beginning
	// of the scene, shortening until the complete phrase has certified TTS
	// word offsets.
	words := strings.Fields(strings.TrimSpace(sceneText))
	if len(words) > 5 {
		words = words[:5]
	}
	for len(words) >= 2 {
		phrase := strings.Join(words, " ")
		if located, ok := locateEditorialPhrase(timing, sceneText, phrase); ok && located.EndUS > located.StartUS && located.EndUS <= timing.DurationUS {
			return capabilityoverlay.TimedAnnotation{
				Text: located.Text, StartUS: located.StartUS, StartMs: located.StartUS / 1000,
				EndMs: (located.EndUS + 999) / 1000, DurationUS: located.EndUS - located.StartUS, Score: 0.5,
			}, true
		}
		words = words[:len(words)-1]
	}
	return capabilityoverlay.TimedAnnotation{}, false
}

// editorialBulletShortPhrase chooses the highest-scoring Rust-certified
// visual highlight from the sentence span summarized by this scene bullet.
// Rust marks only phrases of at most five words visual-eligible, which routes
// the resulting card through the certified short-phrase motion family.
func editorialBulletShortPhrase(scene scriptpkg.EditorialScene, bullet scriptpkg.EditorialSceneBullet) string {
	var chosen string
	bestScore := -1.0
	var sceneFallback string
	sceneFallbackScore := -1.0
	for _, highlight := range scene.Highlights {
		words := strings.Fields(strings.TrimSpace(highlight.Text))
		if !highlight.VisualEligible || len(words) == 0 || len(words) > 5 {
			continue
		}
		if highlight.Score > sceneFallbackScore {
			sceneFallback, sceneFallbackScore = strings.TrimSpace(highlight.Text), highlight.Score
		}
		// Rust may index sentence spans across the complete script. Grounding
		// against this scene's bullet text is the reliable scene-local check.
		if strings.Contains(strings.ToLower(bullet.Text), strings.ToLower(strings.TrimSpace(highlight.Text))) && highlight.Score > bestScore {
			chosen, bestScore = strings.TrimSpace(highlight.Text), highlight.Score
		}
	}
	if chosen == "" {
		return sceneFallback
	}
	return chosen
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
		if scene.GloballyIndexed {
			if bullet.SentenceStart < 0 || bullet.SentenceEnd <= bullet.SentenceStart {
				return false
			}
			continue
		}
		if !validEditorialBullet(scene, bullet, text, transcript) {
			return false
		}
	}
	if len(scene.ChartValues) > 6 {
		return false
	}
	seenChartValues := make(map[float64]struct{}, len(scene.ChartValues))
	for _, value := range scene.ChartValues {
		parsed, ok := parseEditorialChartValue(value)
		if !ok || !strings.Contains(text, value) {
			return false
		}
		if _, duplicate := seenChartValues[parsed]; duplicate {
			return false
		}
		seenChartValues[parsed] = struct{}{}
	}
	for _, highlight := range scene.Highlights {
		if scene.GloballyIndexed {
			if !highlight.VisualEligible || strings.TrimSpace(highlight.Text) == "" || !strings.Contains(strings.ToLower(text), strings.ToLower(strings.TrimSpace(highlight.Text))) {
				return false
			}
			continue
		}
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

func parseEditorialChartValue(value string) (float64, bool) {
	return capabilityoverlay.ParseChartValue(value)
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
		// SentenceStart/End are script-global and the bullet may summarize
		// rather than quote the scene. Display only a separate exact Rust
		// highlight found in this scene and located in its TTS below.
		return bullet.SentenceStart >= 0 && bullet.SentenceEnd > bullet.SentenceStart
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
