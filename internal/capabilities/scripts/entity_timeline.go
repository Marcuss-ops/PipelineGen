// Package scriptgeneration — entity_timeline.go derives the canonical
// EntityTimeline projection for a completed result: every entity occurrence
// the enrichment plane produced for a scene is anchored to the REAL word
// timing of the voiceover actually used (the SpeechTimingArtifact captured
// in the same synthesis stream as the audio) and mapped onto the final
// combined timeline via the scene's canonical offset.
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
// projection for the result's source language. It mirrors
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
	var scenes []capabilityentities.SceneInput
	for i := range result.Scenes {
		scene := &result.Scenes[i]
		if scene.Annotations == nil {
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
		sources := entitySourcesFromAnnotations(scene.Annotations)
		if len(sources) == 0 {
			continue
		}
		segment := result.CanonicalTimeline.Segments[i]
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
		DurationUS: result.CanonicalTimeline.DurationUS,
		Scenes:     scenes,
	})
	if err != nil {
		return err
	}
	result.EntityTimeline = &timeline
	return nil
}

// entitySourcesFromAnnotations projects a scene's annotations onto the
// neutral EntitySource inputs consumed by the capability builder. The rune
// span of the entity's first mention is forwarded so the builder verifies
// the exact text anchor instead of re-deriving it.
func entitySourcesFromAnnotations(ann *scriptpkg.SceneAnnotations) []capabilityentities.EntitySource {
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
