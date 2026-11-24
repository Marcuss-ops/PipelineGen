package workunits

import (
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// runner_scene_bindings.go — direct stock and fixed-media binding projection
// onto the durable Scene surface, plus the extraction toggles and committed
// source text the postprocessor path shares.

// ApplyDurableStockBindings projects direct stock bindings onto the durable
// Scene surface. The legacy postprocessor path performs the equivalent
// projection on SpecScene; script.generate single-item jobs use this runner
// directly and must retain the same mixed-media contract.
func ApplyDurableStockBindings(req contract.GenerateRequest, scenes []contract.Scene) {
	// Overlay-only runs must not acquire or bind scene video: stock folders are
	// base-video input, not overlay input. Leaving the scenes unbound is what
	// keeps the two pipelines independent.
	if req.Render.OverlaysOnly || req.MediaMode == scriptpkg.MediaModeClipOnly || len(req.StockBindings) == 0 {
		return
	}
	body := make([]int, 0, len(scenes))
	for i := range scenes {
		if !scenes[i].ExecutionMode.IsFixedMedia() {
			body = append(body, i)
		}
	}
	for _, input := range req.StockBindings {
		target := -1
		if strings.TrimSpace(input.SceneID) != "" {
			for _, index := range body {
				if scenes[index].ID == input.SceneID {
					target = index
					break
				}
			}
		}
		if target < 0 && input.Index >= 0 && input.Index < len(body) {
			target = body[input.Index]
		}
		if target < 0 {
			continue
		}
		scenes[target].Stock = stockBindingFromInput(input)
		// Stock-only scenes do not receive a clip duration or TTS duration.
		// Project the editorial window onto the durable Scene now so the
		// canonical timeline compiler never has to guess (or reject a valid
		// folder-backed stock scene as duration-less).
		if scenes[target].DurationMS <= 0 && scenes[target].Stock.DurationMs > 0 {
			scenes[target].DurationMS = scenes[target].Stock.DurationMs
			scenes[target].DurationUS = scenes[target].Stock.DurationMs * 1000
		}
	}
}

// stockBindingFromInput is the single projection from the caller's binding
// input onto the durable scene binding. Both the batch preflight and the
// streaming per-scene path use it, so the two cannot diverge.
func stockBindingFromInput(input scriptpkg.StockBindingInput) *scriptpkg.StockBinding {
	return &scriptpkg.StockBinding{
		AssetID: input.AssetID, Name: input.Name, Source: input.Source,
		DriveLink: input.DriveLink, FolderID: input.FolderID,
		FolderLink: input.FolderLink, Score: input.Score, Fallback: input.Fallback,
		PoolSlot: input.PoolSlot,
		StartMs:  input.StartMs, EndMs: input.EndMs, DurationMs: input.EndMs - input.StartMs,
	}
}

// ApplyDurableStockBindingToScene is the streaming counterpart of
// ApplyDurableStockBindings. ORCH-07: the SceneTextReady coordinator is
// submitted one scene at a time, so projecting stock only after the stream
// joins would let the coordinator observe `Stock == nil` and launch Chronon
// renders the final assembler must then discard. Binding the scene BEFORE it
// is committed makes `scene.Stock != nil` an invariant from birth.
//
// bodyOrdinal is the zero-based position of this scene among non-fixed-media
// body scenes, matching the index space ApplyDurableStockBindings uses.
func ApplyDurableStockBindingToScene(req contract.GenerateRequest, scene *contract.Scene, bodyOrdinal int) {
	if scene == nil || scene.ExecutionMode.IsFixedMedia() {
		return
	}
	if req.Render.OverlaysOnly || req.MediaMode == scriptpkg.MediaModeClipOnly || len(req.StockBindings) == 0 {
		return
	}
	for _, input := range req.StockBindings {
		input := input
		matched := false
		if sceneID := strings.TrimSpace(input.SceneID); sceneID != "" {
			matched = sceneID == strings.TrimSpace(scene.ID)
		} else {
			matched = input.Index == bodyOrdinal
		}
		if !matched {
			continue
		}
		scene.Stock = stockBindingFromInput(input)
		// Stock-only scenes do not receive a clip duration or TTS duration:
		// project the editorial window before the coordinator computes render
		// expectations or durations.
		if scene.DurationMS <= 0 && scene.Stock.DurationMs > 0 {
			scene.DurationMS = scene.Stock.DurationMs
			scene.DurationUS = scene.Stock.DurationMs * 1000
		}
		return
	}
}

// fixedMediaClipProjection creates the authoritative clip/audio projection
// for a protected section. A partial playback window applies to each bound
// clip; the zero-value policy has already normalized to the canonical default
// window before this projection is built.
func FixedMediaClipProjection(ids []string, playback scriptpkg.FixedPlaybackPolicy) ([]*contract.ClipReference, []capabilityaudio.AudioIntent, int64) {
	playback = playback.Normalize()
	clips := make([]*contract.ClipReference, 0, len(ids))
	intents := make([]capabilityaudio.AudioIntent, 0, len(ids))
	var durationUS int64
	for i, id := range ids {
		clip := &contract.ClipReference{ID: id}
		windowUS := fixedPlaybackDurationUS(playback)
		intent := capabilityaudio.AudioIntent{
			Mode:                   capabilityaudio.AudioClip,
			ClipAssetID:            id,
			SourceInUS:             playback.SourceInMS * 1000,
			SourceDurationUS:       windowUS,
			TimelineOffsetUS:       durationUS,
			UseOriginalAudio:       playback.AudioMode == scriptpkg.FixedPlaybackOriginalClip,
			ProtectedOriginalAudio: playback.AudioMode == scriptpkg.FixedPlaybackOriginalClip,
		}
		if playback.AudioMode == scriptpkg.FixedPlaybackSilence {
			intent = capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioSilence, TimelineOffsetUS: durationUS, TimelineDurationUS: windowUS}
		}
		if windowUS > 0 {
			intent.TimelineDurationUS = windowUS
			durationUS += windowUS
		}
		clip.SourceInMS = playback.SourceInMS
		clip.SourceOutMS = playback.SourceOutMS
		if i == 0 && playback.AudioMode == scriptpkg.FixedPlaybackOriginalClip {
			clip.AudioAssetID = id
		}
		clips = append(clips, clip)
		intents = append(intents, intent)
	}
	return clips, intents, durationUS
}

// CommittedSegmentSourceText resolves the immutable evidence belonging to a
// committed scene. It intentionally never falls back to the generated
// narration: a missing segment brief remains visible to the enricher as
// missing evidence instead of silently broadening to the global source.
func SceneExtractionToggle(req contract.GenerateRequest, scene contract.Scene) scriptpkg.Toggle {
	segmentToggle := scriptpkg.ToggleDefault
	matchedByID := false
	for _, segment := range req.ScriptParams.Segments {
		if id := strings.TrimSpace(segment.ID); id != "" && id == strings.TrimSpace(scene.ID) {
			segmentToggle = segment.ExtractEntities
			matchedByID = true
			break
		}
	}
	if !matchedByID && scene.Index >= 0 && scene.Index < len(req.ScriptParams.Segments) {
		segment := req.ScriptParams.Segments[scene.Index]
		segmentID := strings.TrimSpace(segment.ID)
		if segmentID == "" || segmentID == strings.TrimSpace(scene.ID) {
			segmentToggle = segment.ExtractEntities
		}
	}
	if segmentToggle == "" || segmentToggle == scriptpkg.ToggleDefault {
		return req.ExtractEntities
	}
	return segmentToggle
}

func CommittedSegmentSourceText(req contract.GenerateRequest, scene contract.Scene) string {
	for _, segment := range req.ScriptParams.Segments {
		if strings.TrimSpace(segment.ID) != "" && strings.TrimSpace(segment.ID) == strings.TrimSpace(scene.ID) && strings.TrimSpace(segment.SourceText) != "" {
			return strings.TrimSpace(segment.SourceText)
		}
	}
	if scene.Index >= 0 && scene.Index < len(req.ScriptParams.Segments) {
		segment := req.ScriptParams.Segments[scene.Index]
		if segmentID := strings.TrimSpace(segment.ID); segmentID == "" || segmentID == strings.TrimSpace(scene.ID) {
			if source := strings.TrimSpace(segment.SourceText); source != "" {
				return source
			}
		}
	}
	return ""
}

// generateSceneTextStreaming drives the streaming SceneTextStreamer, firing
// one SceneCommitted (SceneTextReady) per scene as it is emitted and
