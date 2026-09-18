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
// The projection is fail-closed, exactly like the phrase timing projection:
// a scene that carries both annotations and word timing must ground and
// speak every entity verbatim, or the run fails instead of producing a
// plausible-but-wrong timestamp. Scenes without annotations, or without
// word timing, contribute nothing (legitimate no-op).
package scriptgeneration

import (
	"strings"

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
		source := capabilityentities.EntitySource{
			Name:       name,
			SpokenName: annotationSpokenSurface(sceneText, name, entity.Mentions),
			Type:       strings.TrimSpace(entity.Type),
			Confidence: entity.Confidence,
			TextStart:  -1,
			TextEnd:    -1,
		}
		if len(entity.Mentions) > 0 {
			source.TextStart = entity.Mentions[0].StartRune
			source.TextEnd = entity.Mentions[0].EndRune
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
		return canonical
	}
	mention := mentions[0]
	runes := []rune(text)
	if mention.StartRune < 0 || mention.EndRune <= mention.StartRune || mention.EndRune > len(runes) {
		return canonical
	}
	surface := string(runes[mention.StartRune:mention.EndRune])
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
