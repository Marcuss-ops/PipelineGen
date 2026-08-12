package scriptgeneration

import (
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
)

func ValidateFinalAudioReference(ref FinalAudioReference, plan audio.CompiledAudioPlan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if ref.AudioContractVersion != audio.AudioContractVersion || ref.AudioPlanVersion != plan.Version || ref.PlanSHA256 != plan.PlanSHA256 || ref.FinalAudioSHA256 == "" || ref.Path == "" || !ref.FinalMix || !ref.CopyEligible || ref.Bitrate <= 0 || ref.SizeBytes <= 0 || ref.StartPTS < 0 || ref.DurationMS <= 0 || math.Abs(float64(ref.DurationMS-(plan.DurationUS/1000))) > 40 {
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
// independent offsets on either side of the enqueue boundary.
func CompileCanonicalAudioPlan(result GenerateResult, language Language, profile audio.CanonicalAudioProfile) (audio.CanonicalTimeline, audio.CompiledAudioPlan, audio.ResolvedAudioAssets, error) {
	if len(result.Scenes) == 0 {
		return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, fmt.Errorf("canonical timeline requires scenes")
	}
	timeline, err := compileSceneTimeline(result)
	if err != nil {
		return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, err
	}
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
	for i, scene := range result.Scenes {
		intent := timeline.Segments[i].Audio
		if intent.Mode == audio.AudioVoiceover {
			ref, ok := scene.Voiceover[language]
			if !ok || strings.TrimSpace(ref.ID) == "" || strings.TrimSpace(ref.FilePath) == "" {
				return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, fmt.Errorf("scene %s voiceover asset is missing", scene.ID)
			}
			intent.VoiceoverAssetID = ref.ID
			timeline.Segments[i].Audio = intent
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
	}
	plan, err := audio.Compile(timeline, profile)
	if err != nil {
		return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, err
	}
	return timeline, plan, assets, nil
}

// CompileCanonicalTimeline is the visual-side timing compiler. It calls the
// same scene timeline builder used by audio so source and destination timing
// cannot diverge.
func CompileCanonicalTimeline(result GenerateResult) (audio.CanonicalTimeline, error) {
	if len(result.Scenes) == 0 {
		return audio.CanonicalTimeline{}, fmt.Errorf("canonical timeline requires scenes")
	}
	return compileSceneTimeline(result)
}

func compileSceneTimeline(result GenerateResult) (audio.CanonicalTimeline, error) {
	timeline := audio.CanonicalTimeline{Version: audio.TimelineVersion}
	var startUS int64
	for i, scene := range result.Scenes {
		if scene.Index != i || strings.TrimSpace(scene.ID) == "" || scene.DurationMS <= 0 {
			return audio.CanonicalTimeline{}, fmt.Errorf("scene %d has no canonical index/duration", i)
		}
		durationUS, err := microseconds(scene.DurationMS)
		if err != nil {
			return audio.CanonicalTimeline{}, fmt.Errorf("scene %s duration: %w", scene.ID, err)
		}
		intent := scene.Audio
		if intent.Mode == "" {
			intent.Mode = audio.AudioSilence
		}
		video, err := sceneVideoSegment(scene, intent)
		if err != nil {
			return audio.CanonicalTimeline{}, fmt.Errorf("scene %s video timing: %w", scene.ID, err)
		}
		timeline.Segments = append(timeline.Segments, audio.TimelineSegment{
			ID:              scene.ID,
			Index:           i,
			TimelineStartUS: startUS,
			DurationUS:      durationUS,
			Video:           video,
			Audio:           intent,
		})

		if startUS > math.MaxInt64-durationUS {
			return audio.CanonicalTimeline{}, fmt.Errorf("scene %s timeline duration overflows", scene.ID)
		}
		startUS += durationUS
	}
	timeline.DurationUS = startUS
	if err := timeline.Validate(); err != nil {
		return audio.CanonicalTimeline{}, err
	}
	return timeline, nil
}

func sceneVideoSegment(scene Scene, intent audio.AudioIntent) (audio.VideoSegment, error) {
	if scene.Clip == nil {
		return audio.VideoSegment{}, nil
	}
	if scene.Clip.SourceInMS == 0 && scene.Clip.SourceOutMS == 0 {
		return audio.VideoSegment{AssetID: scene.Clip.ID, SourceInUS: intent.SourceInUS, SourceDurationUS: intent.SourceDurationUS}, nil
	}
	if scene.Clip.SourceInMS < 0 || scene.Clip.SourceOutMS <= scene.Clip.SourceInMS {
		return audio.VideoSegment{}, fmt.Errorf("source range must satisfy 0 <= source_in_ms < source_out_ms")
	}
	inUS, err := microseconds(scene.Clip.SourceInMS)
	if err != nil {
		return audio.VideoSegment{}, fmt.Errorf("source_in_ms: %w", err)
	}
	outUS, err := microseconds(scene.Clip.SourceOutMS)
	if err != nil {
		return audio.VideoSegment{}, fmt.Errorf("source_out_ms: %w", err)
	}
	return audio.VideoSegment{AssetID: scene.Clip.ID, SourceInUS: inUS, SourceDurationUS: outUS - inUS}, nil
}

// CompileCanonicalRenderPlan turns the canonical timeline into the immutable
// integer-frame plan carried by GenerateResult and the render enqueue payload.
// Asset hashes are mandatory for every visual clip.
func CompileCanonicalRenderPlan(result GenerateResult, timeline audio.CanonicalTimeline, jobID, revision string, fps int) (*render.RenderPlan, error) {
	return CompileCanonicalRenderPlanWithFrameRate(result, timeline, jobID, revision, audio.IntegerFrameRate(fps))
}

// CompileCanonicalRenderPlanWithFrameRate is the rational-frame-rate entry
// point. The integer-FPS wrapper above remains only for legacy callers.
func CompileCanonicalRenderPlanWithFrameRate(result GenerateResult, timeline audio.CanonicalTimeline, jobID, revision string, frameRate audio.FrameRate) (*render.RenderPlan, error) {
	manifest := make([]render.AssetManifestEntry, 0)
	seen := make(map[string]struct{})
	for _, scene := range result.Scenes {
		if scene.Clip == nil {
			continue
		}
		clip := scene.Clip
		if strings.TrimSpace(clip.ID) == "" || strings.TrimSpace(clip.Path) == "" || strings.TrimSpace(clip.SHA256) == "" {
			return nil, fmt.Errorf("render plan clip %s requires path and SHA256", scene.ID)
		}
		if _, ok := seen[clip.ID]; ok {
			continue
		}
		seen[clip.ID] = struct{}{}
		frameCount := clip.FrameCount
		if clip.Duration > 0 {
			durationUS, err := microseconds(int64(math.Round(clip.Duration * 1000)))
			if err != nil {
				return nil, fmt.Errorf("render plan clip %s duration: %w", scene.ID, err)
			}
			resolver, err := audio.NewFrameResolver(frameRate)
			if err != nil {
				return nil, fmt.Errorf("render plan clip %s frame rate: %w", scene.ID, err)
			}
			frameCount, err = resolver.FrameCountForDuration(durationUS)
			if err != nil {
				return nil, fmt.Errorf("render plan clip %s frame count: %w", scene.ID, err)
			}
		}
		if frameCount <= 0 {
			resolver, err := audio.NewFrameResolver(frameRate)
			if err != nil {
				return nil, fmt.Errorf("render plan clip %s frame rate: %w", scene.ID, err)
			}
			for _, segment := range timeline.Segments {
				if segment.Video.AssetID != clip.ID || segment.Video.SourceDurationUS <= 0 {
					continue
				}
				endUS := segment.Video.SourceInUS + segment.Video.SourceDurationUS
				if endUS < segment.Video.SourceInUS {
					return nil, fmt.Errorf("render plan clip %s source range overflows", scene.ID)
				}
				endFrame, frameErr := resolver.FrameAt(endUS)
				if frameErr != nil {
					return nil, fmt.Errorf("render plan clip %s source frame count: %w", scene.ID, frameErr)
				}
				if endFrame > frameCount {
					frameCount = endFrame
				}
			}
		}
		if frameCount <= 0 {
			return nil, fmt.Errorf("render plan clip %s requires positive frame_count", scene.ID)
		}
		manifest = append(manifest, render.AssetManifestEntry{AssetID: clip.ID, Path: clip.Path, SHA256: clip.SHA256, FrameCount: frameCount})
	}
	var finalAudio *render.FinalAudioAsset
	if result.FinalAudio != nil {
		finalAudio = &render.FinalAudioAsset{
			AssetID:              result.FinalAudio.AssetID,
			AssetKind:            "final_audio",
			Strategy:             string(audio.FinalAudioCopy),
			Path:                 result.FinalAudio.Path,
			SHA256:               result.FinalAudio.FinalAudioSHA256,
			PlanSHA256:           result.FinalAudio.PlanSHA256,
			AudioContractVersion: result.FinalAudio.AudioContractVersion,
			AudioPlanVersion:     result.FinalAudio.AudioPlanVersion,
			Codec:                result.FinalAudio.Codec,
			Profile:              result.FinalAudio.Profile,
			SampleRate:           result.FinalAudio.SampleRate,
			Channels:             result.FinalAudio.Channels,
			ChannelLayout:        result.FinalAudio.ChannelLayout,
			DurationMS:           result.FinalAudio.DurationMS,
			StartPTS:             result.FinalAudio.StartPTS,
			SizeBytes:            result.FinalAudio.SizeBytes,
			FinalMix:             result.FinalAudio.FinalMix,
			CopyEligible:         result.FinalAudio.CopyEligible,
		}
	}
	outputPath := strings.TrimSpace(result.OutputName)
	if outputPath == "" {
		outputPath = "final.mp4"
	}
	plan, err := render.Compile(render.CompileInput{JobID: jobID, Revision: revision, OutputPath: outputPath, FrameRate: frameRate, Timeline: timeline, FinalAudio: finalAudio, Manifest: manifest})
	if err != nil {
		return nil, err
	}
	return &plan, nil
}
