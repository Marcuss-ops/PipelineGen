package scriptgeneration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func documentSpecSceneSHA256(model *scriptpkg.ModelScriptOutputV1) string {
	if model == nil {
		return ""
	}
	raw, err := json.Marshal(model.SpecScene)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// modelScriptOutputForDocument adapts the runner's durable Scene aggregate to
// the canonical SpecScene envelope consumed by the shared document renderer.
// It is a data adapter only: it does not format HTML and does not alter the
// GenerateResult used by the render pipeline.
func modelScriptOutputForDocument(result *GenerateResult, language Language) *scriptpkg.ModelScriptOutputV1 {
	if result == nil {
		return nil
	}

	spec := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes:  make([]scriptpkg.SpecScene, 0, len(result.Scenes)),
		Render:  result.Render,
	}
	var allText []string
	for _, scene := range result.Scenes {
		text := strings.TrimSpace(scene.Text[language])
		if text != "" {
			allText = append(allText, text)
		}

		converted := scriptpkg.SpecScene{
			ID:          scene.ID,
			Index:       scene.Index,
			Text:        text,
			Kind:        scriptpkg.SceneNarration,
			Bindings:    scriptpkg.SceneBindings{},
			Annotations: scene.Annotations,
		}
		if scene.Clip != nil || len(scene.Clips) > 0 {
			converted.Kind = scriptpkg.SceneClip
		}
		for _, clip := range scene.Clips {
			if clip == nil {
				continue
			}
			converted.Bindings.Clips = append(converted.Bindings.Clips, clipBindingFromReference(clip))
		}
		if scene.Clip != nil {
			clipBinding := clipBindingFromReference(scene.Clip)
			converted.Bindings.Clip = &clipBinding
			if len(converted.Bindings.Clips) == 0 {
				converted.Bindings.Clips = append(converted.Bindings.Clips, *converted.Bindings.Clip)
			}
		}

		voiceover := &scriptpkg.VoiceoverBinding{}
		for lang, audio := range scene.Voiceover {
			if strings.TrimSpace(audio.URL) == "" {
				continue
			}
			if voiceover.Links == nil {
				voiceover.Links = make(map[string]string)
			}
			voiceover.Links[string(lang)] = strings.TrimSpace(audio.URL)
			// Preserve the published timing bundle (timing.json SSOT + optional
			// SRT/VTT links + hashes) per language so the document renderer can
			// surface the original timing links. The word-level array is never
			// inlined here — it stays in the published timing.json.
			if audio.TimingBundle != nil {
				if voiceover.Timing == nil {
					voiceover.Timing = make(map[string]scriptpkg.VoiceoverTimingBinding)
				}
				voiceover.Timing[string(lang)] = *audio.TimingBundle
			}
		}
		if link := voiceover.Links[string(language)]; link != "" {
			voiceover.Link = link
			voiceover.Status = "completed"
		}
		if len(voiceover.Links) > 0 {
			converted.Bindings.Voiceover = voiceover
		}
		spec.Scenes = append(spec.Scenes, converted)
	}

	return &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          strings.Join(allText, "\n\n"),
		SpecScene:     spec,
	}
}

// documentSkeletonInputForScenes projects the scene-text-only skeleton inputs
// for every docs language from the durable scene list. It reads only the final
// scene text (immutable after translation), so it can be captured at
// SceneTextReady and rendered by the prepare branch concurrently with TTS
// without touching the mutable Scene struct.
func documentSkeletonInputForScenes(title string, scenes []Scene, langs []Language) map[Language]DocumentSkeletonInput {
	inputs := make(map[Language]DocumentSkeletonInput, len(langs))
	for _, lang := range langs {
		in := DocumentSkeletonInput{Title: title}
		for i := range scenes {
			in.Scenes = append(in.Scenes, DocumentSceneText{
				ID:    scenes[i].ID,
				Index: scenes[i].Index,
				Text:  strings.TrimSpace(scenes[i].Text[lang]),
			})
		}
		inputs[lang] = in
	}
	return inputs
}

func clipBindingFromReference(clip *ClipReference) scriptpkg.ClipBinding {
	if clip == nil {
		return scriptpkg.ClipBinding{}
	}
	return scriptpkg.ClipBinding{
		ClipID:          clip.ID,
		ClipTitle:       clip.Title,
		DriveLink:       clip.DriveLink,
		StartMs:         clip.SourceInMS,
		EndMs:           clip.SourceOutMS,
		TotalDurationMs: int64(math.Round(clip.Duration * 1000)),
	}
}

// documentAudioSummaryFor resolves the aggregate audio facts (clip totals,
// voiceover totals, counts) once at the capability boundary, so the document
// renderer only formats them. It mirrors the accounting the renderer used to
// perform inline: every clip binding contributes one clip to the count, and
// every voiceover intent contributes its source duration to the voiceover
// total. A clip with no known total duration marks the total as unknown (the
// renderer formats "Unknown" rather than a fabricated sum).
func documentAudioSummaryFor(result *GenerateResult) capabilityaudio.DocumentAudioSummary {
	var s capabilityaudio.DocumentAudioSummary
	if result == nil {
		return s
	}
	s.ClipTotalKnown = true
	for _, scene := range result.Scenes {
		clips := scene.Clips
		if len(clips) == 0 && scene.Clip != nil {
			clips = []*ClipReference{scene.Clip}
		}
		for _, clip := range clips {
			if clip == nil {
				continue
			}
			s.ClipCount++
			d := clip.AssetDuration()
			if !d.Known() || d.DurationUS <= 0 {
				s.ClipTotalKnown = false
				continue
			}
			s.ClipTotalUS += d.DurationUS
		}
	}
	if result.CanonicalTimeline != nil {
		for _, segment := range result.CanonicalTimeline.Segments {
			for _, intent := range segment.EffectiveAudioIntents() {
				if intent.Mode == capabilityaudio.AudioVoiceover {
					s.VoiceoverCount++
					s.VoiceoverTotalUS += intent.SourceDurationUS
				}
			}
		}
	}
	return s
}

// clipAssetMetadataForDocument resolves the canonical clip-asset facts
// (total source duration with provenance) at the capability boundary, so the
// document renderer only formats them. Asset IDs are de-duplicated; an asset
// with no known duration carries an explicit unknown Duration (Known=false),
// which the renderer shows as "Unknown" rather than reconstructing from
// another field or treating it as a real zero length.
func clipAssetMetadataForDocument(result *GenerateResult) []capabilityaudio.ClipAssetMetadata {
	if result == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var out []capabilityaudio.ClipAssetMetadata
	for _, scene := range result.Scenes {
		clips := scene.Clips
		if len(clips) == 0 && scene.Clip != nil {
			clips = []*ClipReference{scene.Clip}
		}
		for _, clip := range clips {
			if clip == nil || strings.TrimSpace(clip.ID) == "" {
				continue
			}
			if _, ok := seen[clip.ID]; ok {
				continue
			}
			seen[clip.ID] = struct{}{}
			out = append(out, capabilityaudio.ClipAssetMetadata{
				AssetID:  clip.ID,
				Duration: clip.AssetDuration(),
			})
		}
	}
	return out
}
