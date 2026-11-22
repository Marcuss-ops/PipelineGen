package scriptgeneration

import (
	"fmt"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// runner_scene_fixed_sections.go — fixed intro/outro sections and segment-mode
// scene adjustments (no voiceover, explicit durations), split from
// runner_scene_materialization.go to stay under the per-file limit.

// applyFixedSections injects protected fixed-media intro/outro sections.
// Fixed sections carry optional display text only; their authoritative output
// is the bound clip media and original clip audio.
func applyFixedSections(req GenerateRequest, scenes []Scene) ([]Scene, error) {
	if req.Intro == nil && req.Outro == nil {
		return scenes, nil
	}
	// Validate already done at envelope validation; re-guard the protected
	// media contract without requiring narration text.
	if req.Intro != nil {
		if !req.Intro.NormalizedPlayback().Valid() {
			return nil, fmt.Errorf("intro.playback must use audio_mode=original_clip or silence with a valid source window")
		}
		ids := req.Intro.NormalizedClipIDs()
		if len(ids) == 0 {
			return nil, fmt.Errorf("intro.clip_ids must contain at least one clip_id")
		}
	}
	if req.Outro != nil {
		if !req.Outro.NormalizedPlayback().Valid() {
			return nil, fmt.Errorf("outro.playback must use audio_mode=original_clip or silence with a valid source window")
		}
		ids := req.Outro.NormalizedClipIDs()
		if len(ids) == 0 {
			return nil, fmt.Errorf("outro.clip_ids must contain at least one clip_id")
		}
	}
	out := make([]Scene, 0, len(scenes)+4)
	if req.Intro != nil {
		ids := req.Intro.NormalizedClipIDs()
		playback := req.Intro.NormalizedPlayback()
		cleanText := req.Intro.EffectiveDisplayText()
		clips, intents, durationUS := fixedMediaClipProjection(ids, playback)
		textMap := map[Language]string{req.SourceLanguage: cleanText}
		intro := Scene{
			ID:            "scene-intro",
			Index:         0,
			Role:          scriptpkg.SceneRoleOpening,
			Text:          textMap,
			DurationUS:    durationUS,
			DurationMS:    durationUS / 1000,
			Clip:          clips[0],
			Clips:         clips,
			ExecutionMode: scriptpkg.SceneExecutionFixedMedia,
			FixedPlayback: &playback,
			Audio:         intents[0],
			AudioIntents:  intents,
		}
		out = append(out, intro)
	}
	out = append(out, scenes...)
	if req.Outro != nil {
		ids := req.Outro.NormalizedClipIDs()
		playback := req.Outro.NormalizedPlayback()
		cleanText := req.Outro.EffectiveDisplayText()
		clips, intents, durationUS := fixedMediaClipProjection(ids, playback)
		textMap := map[Language]string{req.SourceLanguage: cleanText}
		outro := Scene{
			ID:            "scene-outro",
			Index:         0, // reindexed below
			Role:          scriptpkg.SceneRoleClosing,
			Text:          textMap,
			DurationUS:    durationUS,
			DurationMS:    durationUS / 1000,
			Clip:          clips[0],
			Clips:         clips,
			ExecutionMode: scriptpkg.SceneExecutionFixedMedia,
			FixedPlayback: &playback,
			Audio:         intents[0],
			AudioIntents:  intents,
		}
		out = append(out, outro)
	}
	// Reindex and deduplicate IDs.
	seen := make(map[string]struct{}, len(out))
	for i := range out {
		if out[i].ID == "" {
			out[i].ID = fmt.Sprintf("scene-%d", i)
		}
		base := out[i].ID
		for {
			if _, exists := seen[base]; !exists {
				break
			}
			base = fmt.Sprintf("%s-%d", out[i].ID, i)
		}
		seen[base] = struct{}{}
		out[i].ID = base
		out[i].Index = i
	}
	return out, nil
}

// applySegmentNoVoiceoverScenes turns every explicit no_voiceover segment into
// an editorial fixed-media clip cutaway. The scene keeps the database clip and
// its original audio, gets the exact editorial duration, and is excluded from
// TTS. When local rendering is enabled, Chronon renders the selected clip and
// the final worker assembles the resulting segment. This is deliberately
// separate from stock: the clip is the visual, while a stock binding is only
// the background of a narrated scene.
func applySegmentNoVoiceoverScenes(req GenerateRequest, scenes []Scene) {
	for i := range scenes {
		scene := &scenes[i]
		if scene.ExecutionMode.IsFixedMedia() {
			// Protected intro/outro sections are already fixed media; never
			// reinterpret them as body cutaways by positional matching.
			continue
		}
		segment, ok := segmentForScene(req.ScriptParams.Segments, scene.ID, scene.Index)
		if !ok || !segment.NoVoiceover {
			continue
		}
		durationMS := segment.DurationMS
		clips := sceneClips(*scene)
		if len(clips) == 0 && len(segment.ClipIDs) > 0 {
			clip := &ClipReference{ID: strings.TrimSpace(segment.ClipIDs[0])}
			scene.Clip = clip
			scene.Clips = []*ClipReference{clip}
			clips = scene.Clips
		}
		// An omitted editorial duration means the complete source asset. The
		// canonical duration was resolved at the media boundary (probe first,
		// provider metadata second); never invent a short playback window.
		if durationMS <= 0 && len(clips) > 0 && clips[0] != nil {
			if assetDuration := clips[0].AssetDuration(); assetDuration.Known() {
				durationMS = assetDuration.DurationUS / 1000
			}
		}
		if durationMS <= 0 {
			durationMS = scene.DurationMS
		}
		if durationMS <= 0 {
			continue
		}

		scene.ExecutionMode = scriptpkg.SceneExecutionFixedMedia
		// Keep a caller-selected cutaway on the localized render lane whenever
		// rendering is enabled. Marking it NativeClip bypasses Chronon entirely,
		// which drops the requested bounded frame/background treatment and
		// leaves the final worker with the raw database clip.
		scene.NativeClip = !req.Render.Enabled
		scene.Role = scriptpkg.SceneRoleBody
		scene.DurationMS = durationMS
		scene.DurationUS = durationMS * 1000
		// The audio compiler requires the explicit fixed-media playback
		// contract; without it the cutaway is rejected before the final render.
		scene.FixedPlayback = &scriptpkg.FixedPlaybackPolicy{
			AudioMode:   scriptpkg.FixedPlaybackOriginalClip,
			SourceInMS:  0,
			SourceOutMS: durationMS,
		}
		// A cutaway must not inherit narration captions or TTS from the
		// segment brief; its only text surface is the clip's own footage.
		scene.Text = map[Language]string{req.SourceLanguage: ""}
		scene.Voiceover = nil

		var clipID string
		for _, clip := range clips {
			if clip == nil {
				continue
			}
			clip.SourceInMS = 0
			clip.SourceOutMS = durationMS
			clip.DurationUS = durationMS * 1000
			if clip.Duration <= 0 {
				clip.Duration = float64(durationMS) / 1000
			}
			if clipID == "" {
				clipID = strings.TrimSpace(clip.ID)
			}
		}
		if clipID == "" {
			continue
		}

		// Preserve the clip's original audio; prepareClipAudioAssets materializes
		// remote/Drive clip audio via ResolveClipAudioAsset before
		// rebuildFixedMediaTiming seals the fixed-media timeline.
		intent := capabilityaudio.AudioIntent{
			Mode: capabilityaudio.AudioClip, ClipAssetID: clipID,
			SourceInUS: 0, SourceDurationUS: durationMS * 1000,
			TimelineOffsetUS: 0, TimelineDurationUS: durationMS * 1000,
			UseOriginalAudio: true, ProtectedOriginalAudio: true,
		}
		scene.Audio = intent
		scene.AudioIntents = []capabilityaudio.AudioIntent{intent}
	}
}

// applySegmentClipBindings binds caller-selected clips onto their generated
// scenes before voiceover fan-out. Script segments carry the editorial source
// clip independently of the model's prose; without this projection a narrated
// clip segment keeps its text and audio window but has no visual render unit.
func applySegmentClipBindings(segments []scriptpkg.ScriptSegment, scenes []Scene) {
	for i := range scenes {
		scene := &scenes[i]
		if len(sceneClips(*scene)) != 0 {
			continue
		}
		segment, ok := segmentForScene(segments, scene.ID, scene.Index)
		if !ok || len(segment.ClipIDs) == 0 {
			continue
		}
		clips := make([]*ClipReference, 0, len(segment.ClipIDs))
		for _, id := range segment.ClipIDs {
			if id = strings.TrimSpace(id); id != "" {
				clips = append(clips, &ClipReference{ID: id})
			}
		}
		if len(clips) == 0 {
			continue
		}
		if segment.DurationMS > 0 {
			for _, clip := range clips {
				clip.Duration = float64(segment.DurationMS) / 1000
				clip.DurationUS = segment.DurationMS * 1000
				clip.SourceOutMS = segment.DurationMS
			}
			scene.DurationMS = segment.DurationMS
			scene.DurationUS = segment.DurationMS * 1000
			clipAudio := capabilityaudio.AudioIntent{
				Mode: capabilityaudio.AudioClip, ClipAssetID: clips[0].ID,
				SourceDurationUS: segment.DurationMS * 1000,
				TimelineDurationUS: segment.DurationMS * 1000,
				UseOriginalAudio: true,
			}
			scene.Audio = clipAudio
			scene.AudioIntents = []capabilityaudio.AudioIntent{clipAudio}
		}
		scene.Clip = clips[0]
		scene.Clips = clips
	}
}

// applyExplicitSegmentDuration carries a caller's editorial duration onto the
// scene. This is what lets a full-length body clip keep its span while a short
// voiceover is mixed inside it; without it the TTS seal becomes the timeline
// duration. Fixed-media scenes own their playback window and are left alone.
func applyExplicitSegmentDuration(req GenerateRequest, scene *Scene) {
	if scene.ExecutionMode.IsFixedMedia() {
		return
	}
	segment, ok := segmentForScene(req.ScriptParams.Segments, scene.ID, scene.Index)
	if !ok || segment.DurationMS <= 0 {
		return
	}
	scene.DurationMS = segment.DurationMS
}

func segmentForScene(segments []scriptpkg.ScriptSegment, sceneID string, sceneIndex int) (scriptpkg.ScriptSegment, bool) {
	for _, segment := range segments {
		if strings.TrimSpace(segment.ID) != "" && strings.TrimSpace(segment.ID) == strings.TrimSpace(sceneID) {
			return segment, true
		}
	}
	if sceneIndex >= 0 && sceneIndex < len(segments) {
		return segments[sceneIndex], true
	}
	return scriptpkg.ScriptSegment{}, false
}
