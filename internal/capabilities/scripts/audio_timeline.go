package scriptgeneration

import (
	"fmt"
	"math"
	"path/filepath"
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

// ValidateVoiceoverSourceDurations enforces the cert-time invariant
// "M4A probe duration == VO source_duration_us": every voiceover event in the
// canonical audio plan must record a source_duration_us equal to the certified
// probe duration of its scene voiceover (seconds rounded to microseconds),
// unless the certified file is longer than the scene window — the event is then
// legitimately clamped to the window, mirroring CompileCanonicalAudioPlan.
//
// Scenes whose voiceover reference carries no certified probe stay lenient
// (the plan falls back to the scene window at compile time). The check runs at
// certification, where the probe is guaranteed known, so it can only fire when
// the plan's recorded source_duration_us drifted from what the compiler must
// have derived for that scene.
func ValidateVoiceoverSourceDurations(result GenerateResult, language Language, timeline audio.CanonicalTimeline, plan audio.CompiledAudioPlan) error {
	if len(result.Scenes) != len(timeline.Segments) {
		return fmt.Errorf("voiceover source-duration certification: scene/timeline count mismatch (%d != %d)", len(result.Scenes), len(timeline.Segments))
	}
	windowByAsset := make(map[string]int64, len(result.Scenes))
	probeByAsset := make(map[string]int64, len(result.Scenes))
	for i, scene := range result.Scenes {
		ref, ok := scene.Voiceover[language]
		if !ok || strings.TrimSpace(ref.ID) == "" {
			continue
		}
		windowByAsset[ref.ID] = timeline.Segments[i].DurationUS
		if ref.Duration > 0 {
			probeByAsset[ref.ID] = int64(math.Round(ref.Duration * 1_000_000))
		}
	}
	for _, track := range plan.Tracks {
		for _, event := range track.Events {
			if event.Type != audio.EventVoiceover {
				continue
			}
			window, ok := windowByAsset[event.AssetID]
			if !ok {
				return fmt.Errorf("voiceover source-duration certification: plan event %s references unknown voiceover asset %q", event.EventID, event.AssetID)
			}
			probe, probed := probeByAsset[event.AssetID]
			if !probed {
				continue // lenient: no certified probe, window fallback is allowed
			}
			allowed := window
			if probe > 0 && probe < allowed {
				allowed = probe
			}
			if event.SourceDurationUS != allowed {
				return fmt.Errorf("voiceover source-duration certification: asset %s records source_duration_us=%d but the certified probe is %d within a %d window", event.AssetID, event.SourceDurationUS, probe, window)
			}
		}
	}
	return nil
}

// ValidateFinalAudioMirror enforces the cert-time invariant
// "final_audio.m4a == CanonicalTimeline == RenderPlan.FinalAudio": the
// RenderPlan.FinalAudio projection must faithfully mirror every certified
// field of the FinalAudioReference. A silent drop or retype in the projection
// would let the renderer consume data that diverges from the certified asset.
// AssetKind and Strategy are pinned to the canonical copy contract.
func ValidateFinalAudioMirror(ref FinalAudioReference, asset render.FinalAudioAsset) error {
	if asset.AssetID != ref.AssetID ||
		asset.AssetKind != "final_audio" ||
		asset.Strategy != string(audio.FinalAudioCopy) ||
		asset.Path != ref.Path ||
		asset.SHA256 != ref.FinalAudioSHA256 ||
		asset.PlanSHA256 != ref.PlanSHA256 ||
		asset.AudioContractVersion != ref.AudioContractVersion ||
		asset.AudioPlanVersion != ref.AudioPlanVersion ||
		asset.Codec != ref.Codec ||
		asset.Profile != ref.Profile ||
		asset.SampleRate != ref.SampleRate ||
		asset.Channels != ref.Channels ||
		asset.ChannelLayout != ref.ChannelLayout ||
		asset.DurationMS != ref.DurationMS ||
		asset.StartPTS != ref.StartPTS ||
		asset.SizeBytes != ref.SizeBytes ||
		asset.FinalMix != ref.FinalMix ||
		asset.CopyEligible != ref.CopyEligible {
		return fmt.Errorf("render plan final audio %q does not mirror the certified reference", ref.AssetID)
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
	resolved, err := resolvedScenesFor(result, language)
	if err != nil {
		return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, err
	}
	timeline, err := compileResolvedSceneTimeline(resolved)
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
		intents := timeline.Segments[i].EffectiveAudioIntents()
		// COMBINED_TIMELINE scenes carry both original clip audio and the
		// generated voiceover. Merge them only after the voiceover asset has
		// been resolved; the canonical segment remains the single timing SSOT.
		if scene.Clip != nil && scene.Audio.Mode == audio.AudioClip {
			hasVoiceoverIntent := false
			for _, intent := range intents {
				if intent.Mode == audio.AudioVoiceover {
					hasVoiceoverIntent = true
					break
				}
			}
			if !hasVoiceoverIntent {
				if ref, ok := scene.Voiceover[language]; ok && ref.ID != "" {
					intents = append(intents, audio.AudioIntent{Mode: audio.AudioVoiceover, VoiceoverAssetID: ref.ID})
				}
			}
		}
		for j := range intents {
			intent := &intents[j]
			if intent.Mode == audio.AudioVoiceover {
				ref, ok := scene.Voiceover[language]
				if !ok || strings.TrimSpace(ref.ID) == "" || strings.TrimSpace(ref.FilePath) == "" {
					return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, fmt.Errorf("scene %s voiceover asset is missing", scene.ID)
				}
				intent.VoiceoverAssetID = ref.ID
				// The scene duration is the canonical placement window, but
				// the source range must use the actual certified TTS duration.
				// Asking the executor for the whole scene window can exceed a
				// perfectly valid, shorter voiceover file (especially for the
				// deliberately brief intro).
				sourceDurationUS := int64(math.Round(ref.Duration * 1_000_000))
				if sourceDurationUS <= 0 || sourceDurationUS > timeline.Segments[i].DurationUS {
					sourceDurationUS = timeline.Segments[i].DurationUS
				}
				intent.SourceInUS = 0
				intent.SourceDurationUS = sourceDurationUS
				// The voiceover placement on the timeline must be explicit:
				// it starts at the scene origin and occupies the full scene
				// window, while source_duration_us keeps the actual certified
				// TTS file length (which may be shorter than the window).
				intent.TimelineOffsetUS = 0
				intent.TimelineDurationUS = timeline.Segments[i].DurationUS
				if err := addAsset(ref.ID, ref.FilePath); err != nil {
					return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, err
				}
			}
			if intent.Mode == audio.AudioClip {
				clipPath := ""
				clips := scene.Clips
				if len(clips) == 0 && scene.Clip != nil {
					clips = []*ClipReference{scene.Clip}
				}
				for _, clip := range clips {
					if clip != nil && clip.ID == intent.ClipAssetID {
						clipPath = clip.AudioPath
						break
					}
				}
				if strings.TrimSpace(intent.ClipAssetID) == "" || strings.TrimSpace(clipPath) == "" {
					return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, fmt.Errorf("scene %s clip audio asset is missing", scene.ID)
				}
				if err := addAsset(intent.ClipAssetID, clipPath); err != nil {
					return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, err
				}
			}
		}
		timeline.Segments[i].AudioIntents = intents
		if len(intents) > 0 {
			timeline.Segments[i].Audio = intents[0]
		}
	}
	if err := timeline.Validate(); err != nil {
		return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, err
	}
	// The combined timeline is narration-first: the voiceover sits at unity
	// and the original clip audio is ducked underneath it. The decision is
	// recorded on the plan (mix_policy) so the mixer and renderer agree.
	plan, err := audio.CompileWithMixPolicy(timeline, profile, audio.MixVoiceoverWithDuckedClip)
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
	resolved, err := resolvedScenesFor(result, "it")
	if err != nil {
		return audio.CanonicalTimeline{}, err
	}
	return compileResolvedSceneTimeline(resolved)
}

func compileSceneTimeline(result GenerateResult) (audio.CanonicalTimeline, error) {
	resolved, err := resolvedScenesFor(result, "it")
	if err != nil {
		return audio.CanonicalTimeline{}, err
	}
	return compileResolvedSceneTimeline(resolved)
}

func resolvedScenesFor(result GenerateResult, language Language) ([]ResolvedScene, error) {
	if len(result.ResolvedScenes) > 0 {
		return append([]ResolvedScene(nil), result.ResolvedScenes...), nil
	}
	return ResolveScenes(result.Scenes, language)
}

func compileResolvedSceneTimeline(scenes []ResolvedScene) (audio.CanonicalTimeline, error) {
	timeline := audio.CanonicalTimeline{Version: audio.TimelineVersion}
	var startUS int64
	for i, scene := range scenes {
		if scene.Index != i || strings.TrimSpace(scene.ID) == "" {
			return audio.CanonicalTimeline{}, fmt.Errorf("scene %d has no canonical index/duration", i)
		}
		durationUS := scene.DurationUS
		if durationUS <= 0 {
			return audio.CanonicalTimeline{}, fmt.Errorf("scene %s has no canonical duration", scene.ID)
		}
		intents := scene.AudioIntents
		timeline.Segments = append(timeline.Segments, audio.TimelineSegment{
			ID:              scene.ID,
			Index:           i,
			TimelineStartUS: startUS,
			DurationUS:      durationUS,
			Video:           scene.Video,
			VideoSegments:   scene.VideoSegments,
			Audio:           intents[0],
			AudioIntents:    intents,
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
		clips := scene.Clips
		if len(clips) == 0 && scene.Clip != nil {
			clips = []*ClipReference{scene.Clip}
		}
		for _, clip := range clips {
			if clip == nil {
				continue
			}
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
					for _, video := range segment.EffectiveVideoSegments() {
						if video.AssetID != clip.ID || video.SourceDurationUS <= 0 {
							continue
						}
						endUS := video.SourceInUS + video.SourceDurationUS
						if endUS < video.SourceInUS {
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
			}
			if frameCount <= 0 {
				return nil, fmt.Errorf("render plan clip %s requires positive frame_count", scene.ID)
			}
			manifest = append(manifest, render.AssetManifestEntry{AssetID: clip.ID, Path: clip.Path, SHA256: clip.SHA256, FrameCount: frameCount})
		}
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
		if err := ValidateFinalAudioMirror(*result.FinalAudio, *finalAudio); err != nil {
			return nil, err
		}
	}
	outputPath := strings.TrimSpace(result.OutputName)
	if outputPath == "" {
		outputPath = "final.mp4"
	} else if filepath.Ext(outputPath) == "" {
		// Titles are valid logical names but not valid media output paths.
		// The executor needs an extension to select the MP4 muxer, while the
		// caller still gets the human-readable title as the basename.
		outputPath += ".mp4"
	}
	plan, err := render.Compile(render.CompileInput{JobID: jobID, Revision: revision, OutputPath: outputPath, FrameRate: frameRate, Timeline: timeline, FinalAudio: finalAudio, Manifest: manifest})
	if err != nil {
		return nil, err
	}
	return &plan, nil
}
