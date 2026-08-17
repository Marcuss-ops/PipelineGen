package scriptgeneration

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// FinalAudioDurationToleranceUS is the single canonical master/final-audio
// duration tolerance. The AAC encoder may pad the final m4a by up to a frame
// or two (~40ms) beyond the canonical timeline without violating the contract.
// Both ValidateFinalAudioReference (the final-audio reference check) and
// ValidateMasterAudioInvariants (the master invariant) derive from this
// constant, matching the Rust render_audio_probe tolerance (0.040s). The
// copy-only mux keeps its own wider window because it compares the
// frame-aligned video container against the audio master.
const FinalAudioDurationToleranceUS int64 = 40_000

func ValidateFinalAudioReference(ref FinalAudioReference, plan audio.CompiledAudioPlan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if ref.AudioContractVersion != audio.AudioContractVersion || ref.AudioPlanVersion != plan.Version || ref.PlanSHA256 != plan.PlanSHA256 || ref.FinalAudioSHA256 == "" || ref.Path == "" || !ref.FinalMix || !ref.CopyEligible || ref.Bitrate <= 0 || ref.SizeBytes <= 0 || ref.StartPTS < 0 || ref.DurationMS <= 0 || math.Abs(float64(ref.DurationMS-(plan.DurationUS/1000))) > float64(FinalAudioDurationToleranceUS)/1000 {
		return fmt.Errorf("final audio reference does not satisfy canonical contract")
	}
	output := plan.Output
	if ref.Codec != output.Codec || ref.Profile != output.Profile || ref.SampleRate != output.SampleRate || ref.Channels != output.Channels || ref.ChannelLayout != output.ChannelLayout {
		return fmt.Errorf("final audio reference profile is incompatible")
	}
	return nil
}

// ValidateMasterAudioInvariants turns the audio-only master invariants into
// automatic cert-time assertions (never manual checks):
//
//  1. scene contiguity + SUM(scene durations) == CanonicalTimeline.duration_us
//     (scene[0] starts at 0 and scene[i+1] starts where scene[i] ends —
//     enforced by CanonicalTimeline.Validate and re-asserted here);
//
//  2. the compiled plan shares one duration with the canonical timeline;
//
//  3. SUM(voiceover timeline durations) == CanonicalTimeline.duration_us for a
//     narration-driven master: when the plan carries no clip-audio track, the
//     voiceover track (VO + explicit silence) must tile the timeline exactly —
//     contiguous events starting at the origin and ending at the timeline
//     duration, with no gaps;
//
//  4. abs(final_audio.duration_us - CanonicalTimeline.duration_us) <=
//     FinalAudioDurationToleranceUS.
func ValidateMasterAudioInvariants(timeline audio.CanonicalTimeline, plan audio.CompiledAudioPlan, finalAudio FinalAudioReference) error {
	if err := timeline.Validate(); err != nil {
		return fmt.Errorf("master audio invariant: %w", err)
	}
	if err := plan.Validate(); err != nil {
		return fmt.Errorf("master audio invariant: %w", err)
	}
	if plan.DurationUS != timeline.DurationUS {
		return fmt.Errorf("master audio invariant: plan duration %dus != canonical timeline %dus", plan.DurationUS, timeline.DurationUS)
	}
	if err := validateNarrationTimelineTiling(plan, timeline.DurationUS); err != nil {
		return err
	}
	finalUS := finalAudio.DurationUS
	if finalUS <= 0 {
		finalUS = finalAudio.DurationMS * 1000
	}
	delta := finalUS - timeline.DurationUS
	if delta < 0 {
		delta = -delta
	}
	if delta > FinalAudioDurationToleranceUS {
		return fmt.Errorf("master audio invariant: final_audio=%dus diverges from CanonicalTimeline=%dus by %dus (tolerance %dus)", finalUS, timeline.DurationUS, delta, FinalAudioDurationToleranceUS)
	}
	return nil
}

// validateNarrationTimelineTiling enforces, for a narration-driven master
// (a plan with no clip-audio track), that the voiceover track tiles the
// canonical timeline: contiguous VO/silence events from the origin to the
// timeline end. For a pure narration master (no silence either) this is
// exactly SUM(voiceover timeline durations) == CanonicalTimeline.duration_us.
// Clip-driven masters are exempt: the clip track owns part of the coverage.
func validateNarrationTimelineTiling(plan audio.CompiledAudioPlan, durationUS int64) error {
	for _, track := range plan.Tracks {
		if track.Role == audio.TrackClipAudio && len(track.Events) > 0 {
			return nil
		}
	}
	var firstStartUS int64 = -1
	var prevEndUS int64 = -1
	var voEvents, silenceEvents int
	contiguous := true
	for _, track := range plan.Tracks {
		if track.Role != audio.TrackVoiceover {
			continue
		}
		for _, event := range track.Events {
			switch event.Type {
			case audio.EventVoiceover:
				voEvents++
			case audio.EventSilence:
				silenceEvents++
			}
			if firstStartUS < 0 {
				firstStartUS = event.TimelineStartUS
			}
			if prevEndUS >= 0 && event.TimelineStartUS != prevEndUS {
				contiguous = false
			}
			prevEndUS = event.TimelineStartUS + event.DurationUS
		}
	}
	if voEvents+silenceEvents == 0 {
		return fmt.Errorf("master audio invariant: narration-driven master has no voiceover track")
	}
	if firstStartUS != 0 {
		return fmt.Errorf("master audio invariant: voiceover track starts at %dus, want 0", firstStartUS)
	}
	if !contiguous {
		return fmt.Errorf("master audio invariant: voiceover track has gaps")
	}
	if prevEndUS != durationUS {
		return fmt.Errorf("master audio invariant: voiceover track ends at %dus != CanonicalTimeline %dus", prevEndUS, durationUS)
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

// AudioCompileTimings captures the per-stage wall-clock of the combined-audio
// compilation: timeline build, clip/voiceover audio asset preparation, and
// final audio plan compile. It feeds the durable AudioPipelineMetrics fields.
type AudioCompileTimings struct {
	TimelineCompileMS  int64
	ClipAudioPrepareMS int64
	AudioPlanCompileMS int64
}

// CompileCanonicalAudioPlan is the timing-insensitive spelling of
// CompileCanonicalAudioPlanWithTimings, retained for existing callers.
func CompileCanonicalAudioPlan(result GenerateResult, language Language, profile audio.CanonicalAudioProfile) (audio.CanonicalTimeline, audio.CompiledAudioPlan, audio.ResolvedAudioAssets, error) {
	timeline, plan, assets, _, err := CompileCanonicalAudioPlanWithTimings(result, language, profile)
	return timeline, plan, assets, err
}

// CompileCanonicalAudioPlanWithTimings is the sole timing compiler for the
// durable generation workflow. It returns the same artifacts as
// CompileCanonicalAudioPlan plus per-stage timings for the runner.
func CompileCanonicalAudioPlanWithTimings(result GenerateResult, language Language, profile audio.CanonicalAudioProfile) (audio.CanonicalTimeline, audio.CompiledAudioPlan, audio.ResolvedAudioAssets, AudioCompileTimings, error) {
	return compileCanonicalAudioPlanWithTimings(result, language, profile, true)
}

// CompileCanonicalAudioPlanAudioOnly builds the VO-governed canonical scene
// timeline for runs without a video render. The original clip audio is NOT
// dropped: it is clamped to the VO-governed scene window and mixed underneath
// the narration (VOICEOVER_DUCKED_CLIP), so the audio-only master carries the
// same audible clip + ducking contract as the video-bound master. Only the
// video/source windows are excluded, so a long evidence clip never stretches
// a VO-governed scene.
func CompileCanonicalAudioPlanAudioOnly(result GenerateResult, language Language, profile audio.CanonicalAudioProfile) (audio.CanonicalTimeline, audio.CompiledAudioPlan, audio.ResolvedAudioAssets, AudioCompileTimings, error) {
	return compileCanonicalAudioPlanWithTimings(result, language, profile, false)
}

func compileCanonicalAudioPlanWithTimings(result GenerateResult, language Language, profile audio.CanonicalAudioProfile, includeClipAudio bool) (audio.CanonicalTimeline, audio.CompiledAudioPlan, audio.ResolvedAudioAssets, AudioCompileTimings, error) {
	var timings AudioCompileTimings
	if len(result.Scenes) == 0 {
		return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, timings, fmt.Errorf("canonical timeline requires scenes")
	}
	resolved, err := resolvedScenesFor(result, language, includeClipAudio)
	if err != nil {
		return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, timings, err
	}
	if !includeClipAudio {
		// Audio-only keeps clip bindings as evidence metadata but removes
		// their video/source windows from the audio timeline, so a long
		// evidence clip never stretches a VO-governed scene. The original
		// clip audio is retained and clamped to the VO-governed scene window:
		// the audio-only master mixes it underneath the narration with the
		// same ducked-clip policy as the video-bound master.
		for i := range resolved {
			resolved[i].Video = audio.VideoSegment{}
			resolved[i].VideoSegments = nil
			for j := range resolved[i].AudioIntents {
				intent := &resolved[i].AudioIntents[j]
				if intent.Mode != audio.AudioClip {
					continue
				}
				// Scene duration is VO-governed in audio-only: never place
				// more clip audio than the scene window and never read more
				// source than we place.
				if intent.TimelineDurationUS == 0 || intent.TimelineDurationUS > resolved[i].DurationUS {
					intent.TimelineDurationUS = resolved[i].DurationUS
				}
				if intent.SourceDurationUS > intent.TimelineDurationUS {
					intent.SourceDurationUS = intent.TimelineDurationUS
				}
			}
		}
	}
	timelineStarted := time.Now()
	timeline, err := compileResolvedSceneTimeline(resolved)
	timings.TimelineCompileMS = time.Since(timelineStarted).Milliseconds()
	if err != nil {
		return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, timings, err
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
	prepareStarted := time.Now()
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
					return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, timings, fmt.Errorf("scene %s voiceover asset is missing", scene.ID)
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
					return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, timings, err
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
					return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, timings, fmt.Errorf("scene %s clip audio asset is missing", scene.ID)
				}
				if err := addAsset(intent.ClipAssetID, clipPath); err != nil {
					return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, timings, err
				}
			}
		}
		timeline.Segments[i].AudioIntents = intents
		if len(intents) > 0 {
			timeline.Segments[i].Audio = intents[0]
		}
	}
	timings.ClipAudioPrepareMS = time.Since(prepareStarted).Milliseconds()
	if err := timeline.Validate(); err != nil {
		return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, timings, err
	}
	// The combined timeline is narration-first: the voiceover sits at unity
	// and the original clip audio is ducked underneath it. The decision is
	// recorded on the plan (mix_policy) so the mixer and renderer agree.
	planStarted := time.Now()
	// The combined timeline is narration-first: the voiceover sits at unity
	// and the original clip audio is ducked underneath it — for the audio-only
	// master just as for the video-bound master. The decision is recorded on
	// the plan (mix_policy) so the mixer and renderer agree.
	policy := audio.MixVoiceoverWithDuckedClip
	plan, err := audio.CompileWithMixPolicy(timeline, profile, policy)
	timings.AudioPlanCompileMS = time.Since(planStarted).Milliseconds()
	if err != nil {
		return audio.CanonicalTimeline{}, audio.CompiledAudioPlan{}, nil, timings, err
	}
	return timeline, plan, assets, timings, nil
}

// CompileCanonicalTimeline is the visual-side timing compiler. It calls the
// same scene timeline builder used by audio so source and destination timing
// cannot diverge.
func CompileCanonicalTimeline(result GenerateResult) (audio.CanonicalTimeline, error) {
	if len(result.Scenes) == 0 {
		return audio.CanonicalTimeline{}, fmt.Errorf("canonical timeline requires scenes")
	}
	resolved, err := resolvedScenesFor(result, "it", false)
	if err != nil {
		return audio.CanonicalTimeline{}, err
	}
	return compileResolvedSceneTimeline(resolved)
}

func compileSceneTimeline(result GenerateResult) (audio.CanonicalTimeline, error) {
	resolved, err := resolvedScenesFor(result, "it", false)
	if err != nil {
		return audio.CanonicalTimeline{}, err
	}
	return compileResolvedSceneTimeline(resolved)
}

func resolvedScenesFor(result GenerateResult, language Language, clipBound bool) ([]ResolvedScene, error) {
	if len(result.ResolvedScenes) > 0 {
		return append([]ResolvedScene(nil), result.ResolvedScenes...), nil
	}
	return ResolveScenes(result.Scenes, language, result.AudioMode, clipBound)
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
		videos := freezeTailedVideoSegments(scene, durationUS)
		timeline.Segments = append(timeline.Segments, audio.TimelineSegment{
			ID:              scene.ID,
			Index:           i,
			TimelineStartUS: startUS,
			DurationUS:      durationUS,
			Video:           scene.Video,
			VideoSegments:   videos,
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

// freezeTailedVideoSegments returns the scene's visual segments, appending a
// synthetic freeze tail when the canonical scene duration outlasts the real
// clips (a narration longer than its clip). The tail holds the last clip's
// final frame for the remaining window, so the canonical timeline's video
// always covers the scene. This is the single owner of the freeze policy —
// never an implicit black gap guessed by the renderer.
func freezeTailedVideoSegments(scene ResolvedScene, durationUS int64) []audio.VideoSegment {
	videos := scene.VideoSegments
	if len(videos) == 0 && scene.Video.AssetID != "" {
		videos = []audio.VideoSegment{scene.Video}
	}
	if len(videos) == 0 {
		return nil
	}
	var visualEnd int64
	for _, v := range videos {
		visualEnd += v.TimelineDurationUS
	}
	if visualEnd >= durationUS {
		return videos
	}
	last := videos[len(videos)-1]
	return append(append([]audio.VideoSegment(nil), videos...), audio.VideoSegment{
		AssetID:            last.AssetID,
		SourceInUS:         last.SourceInUS + last.SourceDurationUS, // exclusive source end
		TimelineOffsetUS:   visualEnd,
		TimelineDurationUS: durationUS - visualEnd,
		Freeze:             true,
	})
}
