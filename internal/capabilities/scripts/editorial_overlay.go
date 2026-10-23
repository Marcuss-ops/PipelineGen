package scriptgeneration

import (
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// projectEditorialOverlays adds only extractive editorial text whose manifest,
// language, scene identity, verbatim grounding and word-level audio timing all
// agree. Every failure is a no-op: narration/annotations remain the fallback.
func projectEditorialOverlays(result *GenerateResult, language Language, scenes []Scene, resolved []ResolvedScene, inputs []capabilityoverlay.SceneInput, globalCategories map[scriptpkg.OverlayCategory]scriptpkg.Toggle, covered ...map[string]bool) []capabilityoverlay.SceneInput {
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
		// A short-phrase scene has an explicit phrase owner. Editorial titles
		// and bullets would compete for that same text slot and category
		// filtering could then erase the requested phrase when bullets are off.
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
		// The timing is content-addressed to a narration. Prove that its
		// complete word sequence still matches this exact scene text before
		// borrowing any local phrase offsets; a title substring alone is not
		// enough to rule out stale timing from a previous translation.
		if _, err := capabilityaudio.LocatePhrase(*voiceover.Timing, text); err != nil {
			continue
		}
		titleCategoryEnabled := !editorialCategoryDisabled(scene, scriptpkg.OverlayCategoryTitles, globalCategories)
		bulletCategoryEnabled := !editorialCategoryDisabled(scene, scriptpkg.OverlayCategoryBullets, globalCategories)
		titleAllowed := titleCategoryEnabled && editorial.TitleStatus == "resolved" && editorial.Title != nil &&
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
		// When a chapter bullet already owns this scene's opening card, keep
		// only the title: adding the editorial bullet too would duplicate the
		// same visual slot (and often the same text).
		if coveredScenes[editorial.SceneID] {
			continue
		}
		for _, bullet := range editorial.Bullets {
			if !bulletCategoryEnabled {
				break
			}
			if bullet.SentenceStart < 0 || bullet.SentenceEnd <= bullet.SentenceStart {
				continue
			}
			// Use the complete extractive sentence span for the bullet. Besides
			// remaining grounded verbatim, this routes the card through the long
			// phrase lane so camera/3D motions authored for longer copy can render.
			longText := editorialBulletLongPhrase(bullet)
			if longText == "" {
				continue
			}
			if located, ok := locateEditorialPhrase(*voiceover.Timing, text, longText); ok &&
				located.StartUS >= 0 && located.EndUS > located.StartUS && located.EndUS <= voiceover.Timing.DurationUS {
				annotation := editorialTimedAnnotation(located, window[0], "scene_bullet", 0.99)
				inputs[inputIndex].Phrases = append(inputs[inputIndex].Phrases, annotation)
				break // the visual contract is one bullet per scene
			}
		}
		// A fallback opening bullet is allowed only when the analysis explicitly
		// had no title candidate. Never turn a rejected title into a bullet.
		if bulletCategoryEnabled && !shortPhraseScenes[editorial.SceneID] && !hasOpeningSceneBullet(inputs[inputIndex].Phrases) && editorial.Title == nil && editorial.TitleStatus == "unavailable" {
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

func editorialCategoryDisabled(scene Scene, category scriptpkg.OverlayCategory, global map[scriptpkg.OverlayCategory]scriptpkg.Toggle) bool {
	if override, ok := scene.OverlayCategories[category]; ok && override != scriptpkg.ToggleDefault {
		return override == scriptpkg.ToggleDisabled
	}
	return global[category] == scriptpkg.ToggleDisabled
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
		// Blocking overlay rule: a shown callout holds at least three
		// complete words, never a one- or two-word fragment.
		if len(words) < 3 || candidate.DurationUS <= 0 || candidate.EndMs <= candidate.StartMs {
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
	for len(words) >= 3 {
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

// editorialBoundaryFunctionWords guards phrase edges the same way the Rust
// extractor does: a shown callout must never start or end on an article,
// editorialBoundaryStopWords returns the SSOT boundary set for the callout
// edge guard: the language's stop words plus function words from the
// LexiconRegistry (config/lexicons/**). Growing "subito focalizzata" into
// "subito focalizzata sulla" (or "sentenze emesse" into "sentenze emesse
// e") renders a dangling fragment with no sense, so expansion keeps adding
// verbatim words until the phrase ends on a content word. A nil registry
// (binary without lexicon bootstrap) yields an empty set, in which case only
// the hanging-punctuation guard applies.
func editorialBoundaryStopWords(language string) map[string]struct{} {
	lex := linguistics.DefaultLexiconOrNil()
	if lex == nil {
		return nil
	}
	return lex.PhraseStopWords(language)
}

func isEditorialBoundaryFunctionWord(word string, stopWords map[string]struct{}) bool {
	cleaned := strings.ToLower(strings.Trim(strings.TrimSpace(word), ".,;:!?\"'()[]"))
	if cleaned == "" {
		return true
	}
	_, blocked := stopWords[cleaned]
	return blocked
}

// hasHangingPhrasePunct reports a trailing comma, semicolon or colon: the
// callout is cut mid-enumeration ("impronte,") and reads broken on screen.
// A terminal stop (./!/¨/…) instead closes the sentence and is a good ending.
func hasHangingPhrasePunct(word string) bool {
	trimmed := strings.TrimSpace(word)
	if trimmed == "" {
		return true
	}
	last := trimmed[len(trimmed)-1]
	return last == ',' || last == ';' || last == ':'
}

// expandEditorialPhraseToMinimumWords grows an extractive phrase shorter than
// the blocking rule's three-word minimum into a complete snippet of at least
// three words. The extra words are taken verbatim from the scene narration
// right after the phrase, so locateEditorialPhrase still demands exact spoken
// text and certified word timing before anything renders. Growth continues
// past dangling function words (up to the five-word short-phrase ceiling)
// and fails closed — returning the phrase unchanged so downstream drops it —
// when no clean content-word ending exists.
func expandEditorialPhraseToMinimumWords(language, sceneText, phrase string) string {
	trimmed := strings.TrimSpace(phrase)
	words := strings.Fields(trimmed)
	if len(words) == 0 || len(words) >= 3 {
		return trimmed
	}
	stopWords := editorialBoundaryStopWords(language)
	source := strings.Fields(strings.TrimSpace(sceneText))
	for i := 0; i+len(words) <= len(source); i++ {
		match := true
		for j := range words {
			if !strings.EqualFold(source[i+j], words[j]) {
				match = false
				break
			}
		}
		if !match {
			continue
		} // Never end the callout on a function word or a hanging comma:
		// keep growing while the candidate ends dangling, within the
		// short-phrase ceiling of five words the bullet lane renders
		// through.
		for length := 3; length <= 5 && i+length <= len(source); length++ {
			grown := source[i : i+length]
			last := grown[len(grown)-1]
			if isEditorialBoundaryFunctionWord(last, stopWords) || hasHangingPhrasePunct(last) {
				continue
			}
			return strings.Join(grown, " ")
		}
		return trimmed
	}
	return trimmed
}

// editorialBulletLongPhrase returns the complete source sentence span. The
// editor may shorten it later, but the default camera-motion lane needs copy
// long enough to use the certified long-phrase animation catalog.
func editorialBulletLongPhrase(bullet scriptpkg.EditorialSceneBullet) string {
	return strings.TrimSpace(bullet.Text)
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
