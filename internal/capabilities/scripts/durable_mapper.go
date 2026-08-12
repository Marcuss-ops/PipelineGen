package scriptgeneration

import (
	"strings"

	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// durableResultToDomain preserves the canonical SpecScene/voiceover surface
// when the durable runner returns the capability result shape.  The two
// result types intentionally differ, so JSON round-tripping one into the
// other silently loses scenes; keep this translation explicit.
func DurableResultToDomain(in *GenerateResult) *domain.GenerationResult {
	if in == nil {
		return nil
	}
	out := &domain.GenerationResult{
		Title: in.Title, Language: string(inLanguage(in)), VoiceoverGroup: in.VoiceoverGroup, AudioMode: string(in.AudioMode),
		AudioStrategy: string(in.AudioStrategy),
		Output:        domain.ScriptOutput{Text: scenesText(in), WordCount: in.WordCount},
	}
	out.Output.SpecScene.Version = 1
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
		ds := domain.SpecScene{ID: scene.ID, Index: scene.Index, Text: text, Kind: sceneKind(scene)}
		ds.AudioMode = string(scene.Audio.Mode)
		ds.AudioAssetID = scene.Audio.ClipAssetID
		if scene.Clip != nil {
			ds.Bindings.Clip = &domain.ClipBinding{
				ClipID: scene.Clip.ID, ClipTitle: scene.Clip.Title, DriveLink: scene.Clip.DriveLink,
				StartMs: scene.Clip.SourceInMS, EndMs: scene.Clip.SourceOutMS,
				DurationMs: int64(scene.Clip.Duration * 1000),
			}
		}
		if vo, ok := scene.Voiceover[Language(lang)]; ok {
			ds.Bindings.Voiceover = &domain.VoiceoverBinding{Status: "completed", Link: vo.URL, Links: map[string]string{lang: vo.URL}, LocalPath: vo.FilePath, DurationMs: int64(vo.Duration * 1000)}
		} else {
			for _, vo := range scene.Voiceover {
				ds.Bindings.Voiceover = &domain.VoiceoverBinding{Status: "completed", Link: vo.URL, Links: map[string]string{lang: vo.URL}, LocalPath: vo.FilePath, DurationMs: int64(vo.Duration * 1000)}
				break
			}
		}
		out.Output.SpecScene.Scenes = append(out.Output.SpecScene.Scenes, ds)
	}
	if in.FinalAudio != nil {
		fa := in.FinalAudio
		out.FinalAudio = &domain.FinalAudioArtifact{AssetID: fa.AssetID, Path: fa.Path,
			AudioContractVersion: fa.AudioContractVersion, AudioPlanVersion: fa.AudioPlanVersion,
			AudioPlanSHA256: fa.PlanSHA256, FinalAudioSHA256: fa.FinalAudioSHA256,
			Codec: fa.Codec, Profile: fa.Profile, SampleRate: fa.SampleRate, Channels: fa.Channels,
			ChannelLayout: fa.ChannelLayout, Bitrate: fa.Bitrate, DurationMS: fa.DurationMS,
			StartPTS: fa.StartPTS, SizeBytes: fa.SizeBytes, FinalMix: fa.FinalMix, CopyEligible: fa.CopyEligible}
	}
	return out
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
