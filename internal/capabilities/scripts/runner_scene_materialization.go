// Package scriptgeneration — runner_scene_materialization.go: generated-scene
// materialization, fixed-section application and scene-commit emission
// (extracted 2026-09-12 from runner_phase_script.go to keep both halves
// under max_lines_per_file_strict=600, godlike/08).
package scriptgeneration

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	sceneplanner "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func materializeGeneratedScenes(req GenerateRequest, scenes []Scene) []Scene {
	if req.ScriptParams.SingleScene || req.ScriptParams.SegmentWords <= 0 || len(scenes) != 1 {
		return scenes
	}

	text := strings.TrimSpace(scenes[0].Text[req.SourceLanguage])
	if text == "" {
		for _, value := range scenes[0].Text {
			if value = strings.TrimSpace(value); value != "" {
				text = value
				break
			}
		}
	}
	if text == "" {
		return scenes
	}

	// An explicit segment plan is authoritative. Some local text generators
	// return one opaque block (or repeat that block for every requested
	// segment) even though the request contains independent scene identities.
	// Never fan that shared block out to every scene: it would give all scenes
	// the same text_hash and contaminate downstream semantic/provider queries.
	if len(req.ScriptParams.Segments) > 0 {
		paragraphs := nonEmptyParagraphs(req.Source.SourceText)
		out := make([]Scene, 0, len(req.ScriptParams.Segments))
		for i, segment := range req.ScriptParams.Segments {
			segmentText := strings.TrimSpace(segment.SourceText)
			if segmentText == "" && i < len(paragraphs) {
				segmentText = paragraphs[i]
			}
			if segmentText == "" {
				segmentText = strings.TrimSpace(segment.Topic)
			}
			if segmentText == "" {
				return scenes
			}
			id := strings.TrimSpace(segment.ID)
			if id == "" {
				id = fmt.Sprintf("scene-%d", i)
			}
			out = append(out, Scene{
				ID: id, Index: i,
				Text: map[Language]string{req.SourceLanguage: segmentText},
			})
		}
		return out
	}

	paragraphs := nonEmptyParagraphs(req.Source.SourceText)
	n := len(paragraphs)
	wordCount := len(strings.Fields(text))
	if n < 2 {
		n = (wordCount + req.ScriptParams.SegmentWords - 1) / req.ScriptParams.SegmentWords
	}
	if n < 2 {
		return scenes
	}

	planned := sceneplanner.NewSceneSynthesizer().FromProse(text, n)
	if len(planned) < 2 {
		return scenes
	}
	out := make([]Scene, 0, len(planned))
	for _, plannedScene := range planned {
		out = append(out, Scene{
			ID: plannedScene.ID, Index: plannedScene.Index,
			Text: map[Language]string{req.SourceLanguage: plannedScene.Text},
		})
	}
	return out
}

func nonEmptyParagraphs(source string) []string {
	paragraphs := strings.Split(strings.TrimSpace(source), "\n\n")
	out := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if value := strings.TrimSpace(paragraph); value != "" {
			out = append(out, value)
		}
	}
	return out
}

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
			return nil, fmt.Errorf("intro.playback must use audio_mode=original_clip with a valid source window")
		}
		ids := req.Intro.NormalizedClipIDs()
		if len(ids) == 0 || len(ids) > 2 {
			return nil, fmt.Errorf("intro.clip_ids must contain 1 or 2 clip_ids")
		}
	}
	if req.Outro != nil {
		if !req.Outro.NormalizedPlayback().Valid() {
			return nil, fmt.Errorf("outro.playback must use audio_mode=original_clip with a valid source window")
		}
		ids := req.Outro.NormalizedClipIDs()
		if len(ids) == 0 || len(ids) > 2 {
			return nil, fmt.Errorf("outro.clip_ids must contain 1 or 2 clip_ids")
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

// fixedMediaClipProjection creates the authoritative clip/audio projection
// for a protected section. A partial playback window applies to each bound
// clip; a zero window remains unresolved until the clip registry supplies the
// complete source duration.
func fixedMediaClipProjection(ids []string, playback scriptpkg.FixedPlaybackPolicy) ([]*ClipReference, []capabilityaudio.AudioIntent, int64) {
	playback = playback.Normalize()
	clips := make([]*ClipReference, 0, len(ids))
	intents := make([]capabilityaudio.AudioIntent, 0, len(ids))
	var durationUS int64
	for i, id := range ids {
		clip := &ClipReference{ID: id}
		intent := capabilityaudio.AudioIntent{
			Mode:                   capabilityaudio.AudioClip,
			ClipAssetID:            id,
			SourceInUS:             playback.SourceInMS * 1000,
			SourceDurationUS:       fixedPlaybackDurationUS(playback),
			TimelineOffsetUS:       durationUS,
			UseOriginalAudio:       true,
			ProtectedOriginalAudio: true,
		}
		if intent.SourceDurationUS > 0 {
			intent.TimelineDurationUS = intent.SourceDurationUS
			clip.SourceInMS = playback.SourceInMS
			clip.SourceOutMS = playback.SourceOutMS
			durationUS += intent.SourceDurationUS
		}
		if i == 0 {
			clip.AudioAssetID = id
		}
		clips = append(clips, clip)
		intents = append(intents, intent)
	}
	return clips, intents, durationUS
}

func fixedPlaybackDurationUS(playback scriptpkg.FixedPlaybackPolicy) int64 {
	if playback.SourceOutMS <= playback.SourceInMS || playback.SourceOutMS == 0 {
		return 0
	}
	return (playback.SourceOutMS - playback.SourceInMS) * 1000
}

// normalizeGeneratedSceneIdentity repairs model-produced duplicate or missing
// scene identities before downstream VidRush fan-out. A model may return a
// valid number of prose chunks while copying the last scene id/index onto
// multiple chunks; enrichment must never collapse those chunks through an id
// keyed map.
func normalizeGeneratedSceneIdentity(scenes []Scene) {
	seen := make(map[string]struct{}, len(scenes))
	for i := range scenes {
		id := strings.TrimSpace(scenes[i].ID)
		if id == "" {
			id = fmt.Sprintf("scene-%d", i)
		}
		if _, exists := seen[id]; exists {
			base := fmt.Sprintf("scene-%d", i)
			id = base
			for suffix := 1; ; suffix++ {
				if _, collision := seen[id]; !collision {
					break
				}
				id = fmt.Sprintf("%s-%d", base, suffix)
			}
		}
		seen[id] = struct{}{}
		scenes[i].ID = id
		scenes[i].Index = i
	}
}

// emitSceneCommits publishes one SceneCommitted event per stable scene after
// the scene-text stage completes. It is a no-op when no observer is wired.
// Emission happens only on the fresh-generation path (not on resume), so a
// committed scene is reported exactly once per generation attempt.
func (r *Runner) emitSceneCommits(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, scenes []Scene) error {
	for _, scene := range scenes {
		if err := r.emitSceneCommit(ctx, runID, req, exec, scene); err != nil {
			return err
		}
	}
	return nil
}

// emitSceneCommit publishes the SceneCommitted event for a single stable
// scene. It is the per-scene emission behind emitSceneCommits and the
// SceneTextReady(N) boundary used by the streaming path: the commit fires as
// soon as one scene's text is final, never waiting for the whole script.
func (r *Runner) emitSceneCommit(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, scene Scene) error {
	observer := r.sceneCommitObserverFor(runID)
	if observer == nil {
		return nil
	}
	event := NewSceneCommitted(runID, scene, req.SourceLanguage, int64(exec.Attempt))
	if err := observer.OnSceneCommitted(ctx, event); err != nil {
		return fmt.Errorf("scene %q commit: %w", scene.ID, err)
	}
	return nil
}

// generateSceneTextStreaming drives the streaming SceneTextStreamer, firing
// one SceneCommitted (SceneTextReady) per scene as it is emitted and
// accumulating the ordered scene list for the downstream stages. An emit
// error (e.g. a failed SceneCommitObserver) aborts generation immediately.
func (r *Runner) generateSceneTextStreaming(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, streamer SceneTextStreamer, ready *sceneReadyCoordinator) ([]Scene, error) {
	scenes, _, err := r.generateSceneTextStreamingWithTrace(ctx, runID, req, exec, streamer, ready)
	return scenes, err
}

func (r *Runner) generateSceneTextStreamingWithTrace(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, streamer SceneTextStreamer, ready *sceneReadyCoordinator) ([]Scene, scriptpkg.SourceTrace, error) {
	var scenes []Scene
	var sceneMu sync.Mutex
	sceneByIndex := make(map[int]Scene)
	emit := func(scene Scene) error {
		// The SceneTextReady boundary: pin when this scene's text became
		// final so the streaming overlap is durable and provable (scene N's
		// translation/TTS must start before scene N+1's text is ready).
		scene.TextReadyAt = time.Now().UTC()
		sceneMu.Lock()
		sceneByIndex[scene.Index] = scene
		sceneMu.Unlock()
		if err := r.emitSceneCommit(ctx, runID, req, exec, scene); err != nil {
			return err
		}
		if ready != nil {
			ready.submit(scene)
		}
		return nil
	}
	var trace scriptpkg.SourceTrace
	var err error
	if traced, ok := streamer.(SceneTextTraceStreamer); ok {
		trace, err = traced.GenerateSceneTextStreamWithTrace(ctx, req, emit)
	} else {
		err = streamer.GenerateSceneTextStream(ctx, req, emit)
	}
	if err != nil {
		return nil, scriptpkg.SourceTrace{}, err
	}
	sceneMu.Lock()
	indexes := make([]int, 0, len(sceneByIndex))
	for index := range sceneByIndex {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	scenes = make([]Scene, 0, len(indexes))
	for _, index := range indexes {
		scenes = append(scenes, sceneByIndex[index])
	}
	sceneMu.Unlock()
	if len(scenes) == 0 {
		return nil, scriptpkg.SourceTrace{}, fmt.Errorf("generate scene text stream emitted zero scenes")
	}
	return scenes, trace, nil
}
