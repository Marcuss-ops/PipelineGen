package scriptgeneration

import (
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

func ValidateFinalAudioReference(ref FinalAudioReference, plan audio.CompiledAudioPlan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if ref.AudioContractVersion != audio.AudioContractVersion || ref.AudioPlanVersion != plan.Version || ref.PlanSHA256 != plan.PlanSHA256 || ref.FinalAudioSHA256 == "" || ref.Path == "" || !ref.FinalMix || !ref.CopyEligible || ref.Bitrate <= 0 || ref.SizeBytes <= 0 || ref.StartPTS < 0 || ref.DurationMS <= 0 || math.Abs(float64(ref.DurationMS-plan.DurationMS)) > 40 {
		return fmt.Errorf("final audio reference does not satisfy canonical contract")
	}
	output := plan.Output
	if ref.Codec != output.Codec || ref.Profile != output.Profile || ref.SampleRate != output.SampleRate || ref.Channels != output.Channels || ref.ChannelLayout != output.ChannelLayout {
		return fmt.Errorf("final audio reference profile is incompatible")
	}
	return nil
}

// ValidateChunkedVoiceovers enforces the one-to-one scene/language mapping
// required by CHUNKED_VOICEOVER. It is intentionally independent of the
// renderer so an invalid payload cannot reach the remote Velox compute.
func ValidateChunkedVoiceovers(result GenerateResult) error {
	if len(result.Scenes) == 0 {
		return fmt.Errorf("chunked voiceover requires scenes")
	}
	seenScenes := make(map[string]struct{}, len(result.Scenes))
	seenAssets := make(map[string]string)
	for i, scene := range result.Scenes {
		if scene.Index != i || strings.TrimSpace(scene.ID) == "" {
			return fmt.Errorf("scene %d has invalid index or id", i)
		}
		if _, ok := seenScenes[scene.ID]; ok {
			return fmt.Errorf("duplicate scene id %q", scene.ID)
		}
		seenScenes[scene.ID] = struct{}{}
		for lang, text := range scene.Text {
			if strings.TrimSpace(text) == "" {
				continue
			}
			ref, ok := scene.Voiceover[lang]
			if !ok || strings.TrimSpace(ref.ID) == "" || strings.TrimSpace(ref.FilePath) == "" {
				return fmt.Errorf("scene %s language %s has no voiceover asset", scene.ID, lang)
			}
			if previous, ok := seenAssets[ref.ID]; ok {
				return fmt.Errorf("voiceover asset %q is mapped more than once (%s and %s)", ref.ID, previous, scene.ID)
			}
			seenAssets[ref.ID] = scene.ID
		}
		for lang, ref := range scene.Voiceover {
			if strings.TrimSpace(ref.ID) == "" || strings.TrimSpace(ref.FilePath) == "" {
				return fmt.Errorf("scene %s language %s has an invalid voiceover asset", scene.ID, lang)
			}
			if text, ok := scene.Text[lang]; !ok || strings.TrimSpace(text) == "" {
				return fmt.Errorf("scene %s has an extra voiceover mapping for %s", scene.ID, lang)
			}
		}
	}
	return nil
}

// CompileCanonicalAudioPlan is the sole timing compiler for the durable
// generation workflow. Scene order and durations are resolved here once; the
// video and audio consumers must use the returned timeline rather than derive
// their own offsets.
func CompileCanonicalAudioPlan(result GenerateResult, language Language, profile audio.CanonicalAudioProfile) (audio.CanonicalTimeline, audio.CompiledAudioPlan, audio.ResolvedAudioAssets, error) {
	if len(result.Scenes) == 0 {
		return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, fmt.Errorf("canonical timeline requires scenes")
	}
	timeline := audio.CanonicalTimeline{Version: audio.TimelineVersion}
	assets := make(audio.ResolvedAudioAssets, 0, len(result.Scenes)*2)
	seen := make(map[string]struct{})
	addAsset := func(id, path string) error {
		if strings.TrimSpace(id) == "" || strings.TrimSpace(path) == "" {
			return fmt.Errorf("audio asset requires id and path")
		}
		if _, ok := seen[id]; ok {
			return nil
		}
		seen[id] = struct{}{}
		assets = append(assets, audio.ResolvedAudioAsset{AssetID: id, Path: path})
		return nil
	}
	var start int64
	for i, scene := range result.Scenes {
		if scene.Index != i || strings.TrimSpace(scene.ID) == "" || scene.DurationMS <= 0 {
			return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, fmt.Errorf("scene %d has no canonical index/duration", i)
		}
		intent := scene.Audio
		if intent.Mode == audio.AudioVoiceover {
			ref, ok := scene.Voiceover[language]
			if !ok || strings.TrimSpace(ref.ID) == "" || strings.TrimSpace(ref.FilePath) == "" {
				return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, fmt.Errorf("scene %s voiceover asset is missing", scene.ID)
			}
			intent.VoiceoverAssetID = ref.ID
			if err := addAsset(ref.ID, ref.FilePath); err != nil {
				return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, err
			}
		}
		if intent.Mode == audio.AudioClip {
			if scene.Clip == nil || strings.TrimSpace(intent.ClipAssetID) == "" || strings.TrimSpace(scene.Clip.AudioPath) == "" {
				return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, fmt.Errorf("scene %s clip audio asset is missing", scene.ID)
			}
			if err := addAsset(intent.ClipAssetID, scene.Clip.AudioPath); err != nil {
				return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, err
			}
		}
		segment := audio.TimelineSegment{ID: scene.ID, Index: i, TimelineStartMS: start, DurationMS: scene.DurationMS, Audio: intent}
		if scene.Clip != nil {
			segment.Video = audio.VideoSegment{AssetID: scene.Clip.ID, SourceInMS: intent.SourceInMS, SourceOutMS: intent.SourceOutMS}
		}
		timeline.Segments = append(timeline.Segments, segment)
		start += scene.DurationMS
	}
	timeline.DurationMS = start
	plan, err := audio.Compile(timeline, profile)
	if err != nil {
		return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, err
	}
	return timeline, plan, assets, nil
}
