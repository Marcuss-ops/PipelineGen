package contract

import (
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// CloneSpecSceneOutput returns a deep copy of the specscene envelope.
func CloneSpecSceneOutput(s scriptpkg.SpecSceneOutput) scriptpkg.SpecSceneOutput {
	if len(s.Scenes) == 0 {
		return s
	}
	out := scriptpkg.SpecSceneOutput{
		Version:           s.Version,
		Scenes:            make([]scriptpkg.SpecScene, len(s.Scenes)),
		VisualAssignments: append([]mediadomain.VisualAssignment(nil), s.VisualAssignments...),
	}
	for i, sc := range s.Scenes {
		out.Scenes[i] = sc
		if sc.Metadata != nil {
			meta := *sc.Metadata
			meta.Tags = append([]string(nil), sc.Metadata.Tags...)
			meta.Keywords = append([]string(nil), sc.Metadata.Keywords...)
			meta.Sources = append([]scriptpkg.SourceReference(nil), sc.Metadata.Sources...)
			out.Scenes[i].Metadata = &meta
		}
		if sc.Annotations != nil {
			ann := *sc.Annotations
			ann.ImportantPhrases = append([]scriptpkg.AnnotationSpan(nil), sc.Annotations.ImportantPhrases...)
			ann.SpecialNames = append([]scriptpkg.AnnotationSpan(nil), sc.Annotations.SpecialNames...)
			ann.ImportantWords = append([]scriptpkg.AnnotationSpan(nil), sc.Annotations.ImportantWords...)
			ann.Warnings = append([]string(nil), sc.Annotations.Warnings...)
			ann.PrimaryEntities = CloneAnnotatedEntities(sc.Annotations.PrimaryEntities)
			ann.SecondaryEntities = CloneAnnotatedEntities(sc.Annotations.SecondaryEntities)
			out.Scenes[i].Annotations = &ann
		}
		out.Scenes[i].Bindings = CloneSceneBindings(sc.Bindings)
	}
	return out
}

// CloneAnnotatedEntities copies annotated entities and their pointer fields.
func CloneAnnotatedEntities(in []scriptpkg.AnnotatedEntity) []scriptpkg.AnnotatedEntity {
	if in == nil {
		return nil
	}
	out := make([]scriptpkg.AnnotatedEntity, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Mentions = append([]scriptpkg.AnnotationSpan(nil), in[i].Mentions...)
		if in[i].Image != nil {
			image := *in[i].Image
			out[i].Image = &image
		}
	}
	return out
}

// CloneSceneBindings returns a deep copy of scene bindings.
func CloneSceneBindings(b scriptpkg.SceneBindings) scriptpkg.SceneBindings {
	out := scriptpkg.SceneBindings{}
	if len(b.Media) > 0 {
		out.Media = make([]scriptpkg.ResolvedMediaBinding, len(b.Media))
		copy(out.Media, b.Media)
	}
	if len(b.Clips) > 0 {
		out.Clips = make([]scriptpkg.ClipBinding, len(b.Clips))
		copy(out.Clips, b.Clips)
		out.Clip = &out.Clips[0]
	} else if b.Clip != nil {
		c := *b.Clip
		out.Clip = &c
	}
	if b.Image != nil {
		img := *b.Image
		out.Image = &img
	}
	if b.Voiceover != nil {
		v := *b.Voiceover
		if b.Voiceover.Links != nil {
			v.Links = make(map[string]string, len(b.Voiceover.Links))
			for language, link := range b.Voiceover.Links {
				v.Links[language] = link
			}
		}
		if b.Voiceover.Timing != nil {
			v.Timing = make(map[string]scriptpkg.VoiceoverTimingBinding, len(b.Voiceover.Timing))
			for language, timing := range b.Voiceover.Timing {
				v.Timing[language] = timing
			}
		}
		out.Voiceover = &v
	}
	if b.Stock != nil {
		s := *b.Stock
		out.Stock = &s
	}
	return out
}
