package scriptgeneration

import (
	"strings"

	domain "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// durableResultToDomain preserves the canonical SpecScene/voiceover surface
// when the durable runner returns the capability result shape.  The two
// result types intentionally differ, so JSON round-tripping one into the
// other silently loses scenes; keep this translation explicit.
func DurableResultToDomain(in *GenerateResult) *domain.GenerationResult {
	if in == nil {
		return nil
	}
	outputText := strings.TrimSpace(in.Output.Text)
	if outputText == "" {
		outputText = scenesText(in)
	}
	wordCount := in.Output.WordCount
	if wordCount <= 0 {
		wordCount = in.WordCount
	}
	if wordCount <= 0 {
		wordCount = len(strings.Fields(outputText))
	}
	out := &domain.GenerationResult{
		Title: in.Title, Language: string(inLanguage(in)), VoiceoverGroup: in.VoiceoverGroup, AudioMode: string(in.AudioMode),
		AudioStrategy: string(in.AudioStrategy),
		Source:        in.SourceTrace,
		Output:        domain.ScriptOutput{Text: outputText, WordCount: wordCount},
	}
	out.Output.SpecScene.Version = 1
	out.Output.SpecScene.Render = in.Render
	out.Output.SpecScene.Render.Normalize()
	out.Output.SpecScene.Scenes = make([]domain.SpecScene, 0, len(in.Scenes))
	lang := string(inLanguage(in))
	for _, scene := range in.Scenes {
		text := scene.Text[Language(lang)]
		if text == "" {
			for _, candidate := range scene.Text {
				text = candidate
				break
			}
		}
		ds := domain.SpecScene{ID: scene.ID, Index: scene.Index, Text: text, Kind: sceneKind(scene), ExecutionMode: scene.ExecutionMode, FixedPlayback: cloneFixedPlayback(scene.FixedPlayback)}
		ds.AudioMode = string(scene.Audio.Mode)
		ds.AudioAssetID = scene.Audio.ClipAssetID
		if scene.FixedPlayback != nil {
			ds.AudioMode = "CLIP_AUDIO"
			ds.AudioAssetID = scene.Audio.ClipAssetID
			ds.AudioSourceInMS = scene.FixedPlayback.SourceInMS
			ds.AudioSourceOutMS = scene.FixedPlayback.SourceOutMS
		}
		if scene.Clip != nil {
			ds.Bindings.Clip = &domain.ClipBinding{
				ClipID: scene.Clip.ID, ClipTitle: scene.Clip.Title, DriveLink: scene.Clip.DriveLink,
				StartMs: scene.Clip.SourceInMS, EndMs: scene.Clip.SourceOutMS,
				DurationMs: int64(scene.Clip.Duration * 1000),
			}
		}
		if vo, ok := scene.Voiceover[Language(lang)]; ok {
			ds.Bindings.Voiceover = durableVoiceoverBinding(lang, vo)
		} else {
			for _, vo := range scene.Voiceover {
				ds.Bindings.Voiceover = durableVoiceoverBinding(lang, vo)
				break
			}
		}
		out.Output.SpecScene.Scenes = append(out.Output.SpecScene.Scenes, ds)
	}
	if in.FinalAudio != nil {
		fa := in.FinalAudio
		out.FinalAudio = &domain.FinalAudioArtifact{AssetID: fa.AssetID, Path: fa.Path, DriveLink: fa.DriveLink, Container: fa.Container,
			AudioContractVersion: fa.AudioContractVersion, AudioPlanVersion: fa.AudioPlanVersion,
			AudioPlanSHA256: fa.PlanSHA256, FinalAudioSHA256: fa.FinalAudioSHA256,
			Codec: fa.Codec, Profile: fa.Profile, SampleRate: fa.SampleRate, Channels: fa.Channels,
			ChannelLayout: fa.ChannelLayout, Bitrate: fa.Bitrate, DurationUS: fa.DurationUS, DurationMS: fa.DurationMS,
			StartPTS: fa.StartPTS, SizeBytes: fa.SizeBytes, FinalMix: fa.FinalMix, CopyEligible: fa.CopyEligible}
	}
	// The durable entity aggregate mirrors the legacy Artifacts.Entities
	// block so the persisted domain surface and the wire result agree on the
	// same typed persons/places/concepts projection.
	if in.Entities != nil {
		out.Artifacts.Entities = in.Entities
	}
	return out
}

// durableVoiceoverBinding projects one durable AudioReference onto the legacy
// domain VoiceoverBinding surface. The published timing bundle (timing.json
// SSOT + optional SRT/VTT links + hashes) is preserved per language so the
// legacy domain envelope exposes the same timing references as the durable
// capability result.
func durableVoiceoverBinding(lang string, vo AudioReference) *domain.VoiceoverBinding {
	binding := &domain.VoiceoverBinding{
		Status:     "completed",
		Link:       vo.URL,
		Links:      map[string]string{lang: vo.URL},
		LocalPath:  vo.FilePath,
		DurationMs: int64(vo.Duration * 1000),
	}
	if vo.TimingBundle != nil {
		binding.Timing = map[string]domain.VoiceoverTimingBinding{lang: *vo.TimingBundle}
	}
	return binding
}

func inLanguage(in *GenerateResult) Language {
	for _, scene := range in.Scenes {
		for lang := range scene.Text {
			return lang
		}
	}
	return "it"
}

func sceneKind(scene Scene) domain.SceneKind {
	if scene.ExecutionMode.IsFixedMedia() {
		switch scene.ID {
		case "scene-intro":
			return domain.SceneIntro
		case "scene-outro":
			return domain.SceneOutro
		default:
			return domain.SceneClip
		}
	}
	if scene.Clip != nil {
		return domain.SceneClip
	}
	if scene.Index == 0 {
		return domain.SceneIntro
	}
	return domain.SceneNarration
}

func scenesText(r *GenerateResult) string {
	parts := make([]string, 0, len(r.Scenes))
	for _, scene := range r.Scenes {
		for _, text := range scene.Text {
			if strings.TrimSpace(text) != "" {
				parts = append(parts, text)
				break
			}
		}
	}
	return strings.Join(parts, "\n\n")
}
