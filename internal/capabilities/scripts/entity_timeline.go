// Package scriptgeneration — entity_timeline.go derives the canonical
// EntityTimeline projection for a completed result: every entity occurrence
// the enrichment plane produced for a scene is anchored to the REAL word
// timing of the voiceover actually used (the SpeechTimingArtifact captured
// in the same synthesis stream as the audio) and mapped onto the final
// combined timeline via the scene's canonical offset.
//
// Entity repeat policy (explicit): ONCE_PER_SCENE. Each entity is projected
// as exactly ONE EntitySource per scene, anchored to its first verbatim
// mention; rendering the same person three times in thirty seconds is
// deliberately avoided. "Every mention" rendering is NOT part of this
// contract — the projection surface is the entity's first renderable
// occurrence, and consumers must not assume per-mention events.
//
// Entity candidates that cannot be grounded in the captured speech are
// omitted. A missing optional overlay must not fail an otherwise valid run,
// and no timestamp is fabricated for an unspoken candidate.
package scriptgeneration

import (
	"strings"
	"unicode"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// compileResultEntityTimeline derives the deterministic entity→timestamp
// projection for one language. It mirrors
// compileResultPhraseTimings: per-scene inputs come from the scene's
// annotations (NLP output), the scene's voiceover word timing (the actual
// synthesis stream) and the scene's canonical timeline offset.
//
// Nil when no scene carries both annotations and word timing. Fail-closed
// when any scene carries both: every entity must be grounded in the scene
// text and spoken verbatim in the voiceover, or the projection aborts.
func compileResultEntityTimeline(result *GenerateResult, language Language) error {
	if result == nil || result.CanonicalTimeline == nil {
		return nil
	}
	resolved, err := overlayResolvedScenesFor(*result, language)
	if err != nil {
		return err
	}
	resolvedByID := make(map[string]ResolvedScene, len(resolved))
	var durationUS int64
	for _, scene := range resolved {
		resolvedByID[scene.ID] = scene
		if end := scene.TimelineStartUS + scene.DurationUS; end > durationUS {
			durationUS = end
		}
	}
	var scenes []capabilityentities.SceneInput
	for i := range result.Scenes {
		scene := &result.Scenes[i]
		annotations := annotationsForLanguage(*scene, language, result.SourceLanguage)
		if annotations == nil {
			continue
		}
		ref, ok := scene.Voiceover[language]
		if !ok || ref.Timing == nil {
			continue
		}
		text := strings.TrimSpace(scene.Text[language])
		if text == "" {
			continue
		}
		sources := entitySourcesFromAnnotations(annotations, text)
		// A translated annotation can outlive the exact surface that produced
		// it (for example when the final translation is regenerated). Keep the
		// low-level projection available for compatibility/audit callers, but do
		// not pass an ungrounded entity into the timeline builder: it would fail
		// the text gate and poison the whole localized plan. Phrase grounding is
		// independent and remains available for this language.
		grounded := sources[:0]
		for _, source := range sources {
			if source.TextStart >= 0 && source.TextEnd > source.TextStart {
				grounded = append(grounded, source)
			}
		}
		sources = grounded
		if len(sources) == 0 {
			continue
		}
		// Entity annotations are candidates, not a reason to abort generation.
		// Keep only names actually present in the captured voiceover timing;
		// omitted candidates simply cannot receive a defensible timestamp.
		spoken := sources[:0]
		for _, source := range sources {
			name := strings.TrimSpace(source.SpokenName)
			if name == "" {
				name = source.Name
			}
			if _, err := capabilityaudio.LocatePhrase(*ref.Timing, name); err == nil {
				spoken = append(spoken, source)
			}
		}
		sources = spoken
		if len(sources) == 0 {
			continue
		}
		segment, ok := resolvedByID[scene.ID]
		if !ok {
			continue
		}
		scenes = append(scenes, capabilityentities.SceneInput{
			SceneID:          scene.ID,
			SceneIndex:       i,
			Text:             text,
			VoiceoverAssetID: ref.ID,
			TimelineStartUS:  segment.TimelineStartUS,
			Timing:           *ref.Timing,
			Entities:         sources,
		})
	}
	if len(scenes) == 0 {
		return nil
	}
	timeline, err := capabilityentities.BuildEntityTimeline(capabilityentities.BuildInput{
		Language:   string(language),
		DurationUS: durationUS,
		Scenes:     scenes,
	})
	if err != nil {
		return err
	}
	result.EntityTimeline = &timeline
	return nil
}

// annotationsForLanguage returns only semantic annotations grounded in the
// requested language. The source annotation remains the compatibility surface
// for its own language; a translated render never reuses source-language spans
// when translated NLP is missing.
func annotationsForLanguage(scene Scene, language Language, sourceLanguage ...Language) *scriptpkg.SceneAnnotations {
	if localized := scene.LocalizedAnnotations[language]; localized != nil {
		return localized
	}
	if scene.Annotations == nil {
		return nil
	}
	annotationLanguage := strings.TrimSpace(scene.Annotations.Language)
	if language == "" || strings.EqualFold(annotationLanguage, string(language)) {
		return scene.Annotations
	}
	// An unlabelled annotation is legacy source-language data. It may be used
	// for the source voiceover only; never project it onto a translated timing
	// stream, where the source entity surface can be absent or translated.
	if annotationLanguage == "" && len(sourceLanguage) > 0 && strings.EqualFold(string(sourceLanguage[0]), string(language)) {
		return scene.Annotations
	}
	return nil
}

// entityRepeatPolicy is the explicit product policy for entity visual
// repetition: each entity is rendered at most once per scene (first
// renderable occurrence). See the package doc for the full contract.
const entityRepeatPolicy = "once_per_scene"

// entitySourcesFromAnnotations projects a scene's annotations onto the
// neutral EntitySource inputs consumed by the capability builder. It emits
// exactly ONE source per entity (entityRepeatPolicy = once_per_scene): the
// rune span of the entity's first mention is forwarded so the builder
// verifies the exact text anchor instead of re-deriving it. Later mentions
// of the same entity in the scene are intentionally NOT projected as
// separate sources.
func entitySourcesFromAnnotations(ann *scriptpkg.SceneAnnotations, sceneText string) []capabilityentities.EntitySource {
	if ann == nil {
		return nil
	}
	var out []capabilityentities.EntitySource
	appendEntity := func(entity scriptpkg.AnnotatedEntity) {
		name := strings.TrimSpace(entity.CanonicalName)
		if name == "" {
			return
		}
		// CanonicalName is the identity key and may remain in the source
		// language. For translated annotations, the first mention is the
		// grounded surface that actually exists in the localized narration
		// (for example source "South Africa" -> Italian "Sud Africa"). Use
		// that surface for the text/timing gates without changing the identity
		// carried by the annotation itself.
		spokenName := annotationSpokenSurface(sceneText, name, entity.Mentions)
		groundedName := name
		if _, ok := findLocalizedEntitySpan(sceneText, groundedName); !ok && strings.TrimSpace(spokenName) != "" {
			if _, mentionOK := findLocalizedEntitySpan(sceneText, spokenName); mentionOK {
				groundedName = spokenName
			}
		}
		source := capabilityentities.EntitySource{
			Name:       groundedName,
			SpokenName: spokenName,
			Type:       strings.TrimSpace(entity.Type),
			Confidence: entity.Confidence,
			TextStart:  -1,
			TextEnd:    -1,
		}
		// Always forward a span freshly resolved against this language's
		// text. Serialized mention offsets can be stale after translation
		// (and a joined form such as "Sudafrica" changes the rune width),
		// so forwarding the old offsets would make a correctly grounded
		// entity fail the builder's explicit text gate.
		if span, ok := findLocalizedEntitySpan(sceneText, groundedName); ok {
			source.TextStart = span.StartRune
			source.TextEnd = span.EndRune
		}
		out = append(out, source)
	}
	for _, entity := range ann.PrimaryEntities {
		appendEntity(entity)
	}
	for _, entity := range ann.SecondaryEntities {
		appendEntity(entity)
	}
	return out
}

// annotationSpokenSurface returns the exact text at the grounded mention span
// for the TTS lookup. The annotation's canonical identity is deliberately not
// used to reconstruct a localized surface (for example "Mike Tyson" must not
// replace the German spoken "Mike Tysons"). For legacy source annotations
// whose span ends before an English possessive suffix, retain that suffix.
func annotationSpokenSurface(text, canonical string, mentions []scriptpkg.AnnotationSpan) string {
	if len(mentions) == 0 {
		if span, ok := findLocalizedEntitySpan(text, canonical); ok {
			return span.Text
		}
		return canonical
	}
	mention := mentions[0]
	runes := []rune(text)
	if mention.StartRune >= 0 && mention.EndRune > mention.StartRune && mention.EndRune <= len(runes) {
		surface := string(runes[mention.StartRune:mention.EndRune])
		// An annotation span is usable only when it still points at the
		// annotation's own surface. Translation rewrites can leave stale
		// offsets; never accept the unrelated text at that offset.
		if strings.TrimSpace(mention.Text) == "" || strings.EqualFold(surface, mention.Text) || strings.EqualFold(surface, canonical) {
			end := mention.EndRune
			if strings.EqualFold(surface, canonical) && end < len(runes) && (runes[end] == '\'' || runes[end] == '’') {
				end++
				if end < len(runes) && (runes[end] == 's' || runes[end] == 'S') {
					end++
				}
				surface = string(runes[mention.StartRune:end])
			}
			return surface
		}
	}
	for _, candidate := range []string{mention.Text, canonical} {
		if span, ok := findLocalizedEntitySpan(text, candidate); ok {
			return span.Text
		}
	}
	return canonical
}

// findLocalizedEntitySpan finds a candidate in the requested text while
// tolerating localization orthography that joins or separates words (for
// example English/annotation "South Africa" versus Italian "Sudafrica").
// Matching ignores Unicode punctuation and spacing, but the returned span is
// the exact original text, so downstream TTS lookup and text grounding remain
// verbatim. No transliteration or semantic translation is invented here.
func findLocalizedEntitySpan(text, candidate string) (scriptpkg.AnnotationSpan, bool) {
	want := make([]rune, 0, len([]rune(candidate)))
	for _, r := range []rune(strings.ToLower(strings.TrimSpace(candidate))) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			want = append(want, r)
		}
	}
	if len(want) == 0 {
		return scriptpkg.AnnotationSpan{}, false
	}
	runes := []rune(text)
	for start, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			continue
		}
		matched := make([]rune, 0, len(want))
		end := start
		for ; end < len(runes) && len(matched) < len(want); end++ {
			if unicode.IsLetter(runes[end]) || unicode.IsNumber(runes[end]) {
				matched = append(matched, unicode.ToLower(runes[end]))
			}
		}
		if len(matched) != len(want) || string(matched) != string(want) {
			continue
		}
		return scriptpkg.AnnotationSpan{
			Text: string(runes[start:end]), StartRune: start, EndRune: end,
		}, true
	}
	return scriptpkg.AnnotationSpan{}, false
}
