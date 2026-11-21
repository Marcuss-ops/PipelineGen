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
	"sync/atomic"
	"time"

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
				DurationMS: segment.DurationMS,
				Text:       map[Language]string{req.SourceLanguage: segmentText},
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

// importantPhraseHintOwnersAvailable reports whether every explicit phrase
// hint has exactly one segment brief that owns it. That owner lets the
// streaming path finalize the scene text before any translation or TTS sees it.
func importantPhraseHintOwnersAvailable(req GenerateRequest) bool {
	if len(req.MediaPlan.Extraction.ImportantPhrases) == 0 {
		return true
	}
	segments := req.ScriptParams.Segments
	if len(segments) == 0 {
		return false
	}
	for _, raw := range req.MediaPlan.Extraction.ImportantPhrases {
		hint := strings.TrimSpace(raw)
		if hint == "" {
			continue
		}
		owners := 0
		for _, segment := range segments {
			if strings.Contains(strings.ToLower(segment.SourceText), strings.ToLower(hint)) ||
				strings.Contains(strings.ToLower(segment.Topic), strings.ToLower(hint)) {
				owners++
			}
		}
		if owners != 1 {
			return false
		}
	}
	return true
}

// applyOwnedImportantPhraseHints finalizes this scene's caller-owned phrase
// hints before SceneTextReady. It mirrors the batch materializer's punctuation
// behavior and only touches hints whose unique brief owner is this scene.
func applyOwnedImportantPhraseHints(req GenerateRequest, scene Scene) Scene {
	if len(req.MediaPlan.Extraction.ImportantPhrases) == 0 || len(req.ScriptParams.Segments) == 0 {
		return scene
	}
	segmentIndex := scene.Index
	for i, segment := range req.ScriptParams.Segments {
		if segment.ID != "" && segment.ID == scene.ID {
			segmentIndex = i
			break
		}
	}
	if segmentIndex < 0 || segmentIndex >= len(req.ScriptParams.Segments) {
		return scene
	}
	owner := req.ScriptParams.Segments[segmentIndex]
	if scene.Text == nil {
		scene.Text = make(map[Language]string)
	}
	text := strings.TrimSpace(scene.Text[req.SourceLanguage])
	for _, raw := range req.MediaPlan.Extraction.ImportantPhrases {
		hint := strings.TrimSpace(raw)
		if hint == "" ||
			(!strings.Contains(strings.ToLower(owner.SourceText), strings.ToLower(hint)) &&
				!strings.Contains(strings.ToLower(owner.Topic), strings.ToLower(hint))) ||
			strings.Contains(strings.ToLower(text), strings.ToLower(hint)) {
			continue
		}
		if text != "" {
			text += " "
		}
		text += hint
		if !strings.HasSuffix(text, ".") && !strings.HasSuffix(text, "!") && !strings.HasSuffix(text, "?") {
			text += "."
		}
	}
	scene.Text[req.SourceLanguage] = text
	return scene
}

// materializeVerbatimSourceTextScenes builds the source-language scene list
// directly from caller-provided segment text. This path is used when a caller
// is translating or processing an already-authored script and must preserve
// every scene's narration without a model rewrite. Clip sources are supported
// when their explicit segments also bind the visual clips used by each scene.
func materializeVerbatimSourceTextScenes(req GenerateRequest) ([]Scene, error) {
	if req.Source.Type != SourceText && req.Source.Type != SourceClips {
		return nil, fmt.Errorf("source_text_verbatim requires a text or clips source")
	}
	if len(req.ScriptParams.Segments) == 0 {
		return nil, fmt.Errorf("source_text_verbatim requires explicit script segments")
	}
	if req.SourceLanguage == "" {
		return nil, fmt.Errorf("source_text_verbatim requires a source language")
	}

	scenes := make([]Scene, 0, len(req.ScriptParams.Segments))
	seen := make(map[string]struct{}, len(req.ScriptParams.Segments))
	for i, segment := range req.ScriptParams.Segments {
		id := strings.TrimSpace(segment.ID)
		if id == "" {
			return nil, fmt.Errorf("source_text_verbatim segment %d has no ID", i)
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("source_text_verbatim has duplicate segment ID %q", id)
		}
		seen[id] = struct{}{}
		if strings.TrimSpace(segment.SourceText) == "" {
			return nil, fmt.Errorf("source_text_verbatim segment %q has no source_text", id)
		}
		scenes = append(scenes, Scene{
			ID:                 id,
			Index:              i,
			DurationMS:         segment.DurationMS,
			ShortPhraseOverlay: segment.ShortPhraseOverlay,
			OverlayCategories:  scriptpkg.CloneOverlayCategories(segment.OverlayCategories),
			Text:               map[Language]string{req.SourceLanguage: segment.SourceText},
		})
	}
	return scenes, nil
}

// sceneForSegment returns the eligible scene that corresponds to the given
// brief: matched by scene ID first, then by position, and finally the first
// eligible scene. This keeps a hint on the scene that owns its brief.
func sceneForSegment(scenes []Scene, eligible []int, segment scriptpkg.ScriptSegment, segmentIndex int) int {
	for _, index := range eligible {
		if segment.ID != "" && scenes[index].ID == segment.ID {
			return index
		}
	}
	if segmentIndex < len(eligible) {
		return eligible[segmentIndex]
	}
	return eligible[0]
}

func ensureRequestedImportantPhrases(req GenerateRequest, scenes []Scene) []Scene {
	// Verbatim source segments are authoritative. For generated narration,
	// callers require their requested phrase surfaces to survive generation so
	// downstream phrase overlays can ground them literally.
	if req.ScriptParams.SourceTextVerbatim {
		return scenes
	}
	hints := req.MediaPlan.Extraction.ImportantPhrases
	if len(hints) == 0 || len(scenes) == 0 {
		return scenes
	}
	eligible := make([]int, 0, len(scenes))
	for i := range scenes {
		if !scenes[i].ExecutionMode.IsFixedMedia() {
			eligible = append(eligible, i)
		}
	}
	if len(eligible) == 0 {
		return scenes
	}
	fallbackCursor := 0
	for _, rawHint := range hints {
		hint := strings.TrimSpace(rawHint)
		if hint == "" {
			continue
		}
		found := false
		for _, index := range eligible {
			if strings.Contains(strings.ToLower(scenes[index].Text[req.SourceLanguage]), strings.ToLower(hint)) {
				found = true
				break
			}
		}
		if found {
			continue
		}

		index := -1
		for i, segment := range req.ScriptParams.Segments {
			if strings.Contains(strings.ToLower(segment.SourceText), strings.ToLower(hint)) ||
				strings.Contains(strings.ToLower(segment.Topic), strings.ToLower(hint)) {
				index = sceneForSegment(scenes, eligible, segment, i)
				break
			}
		}
		if index < 0 {
			// Unowned hints used to pile into the first scene, where phrase
			// timing correctly rejected their overlapping windows. Spread only
			// these fallback hints over eligible scenes; segment-owned hints
			// continue to follow their matching segment.
			index = eligible[fallbackCursor%len(eligible)]
			fallbackCursor++
		}
		text := strings.TrimSpace(scenes[index].Text[req.SourceLanguage])
		if text != "" {
			text += " "
		}
		text += hint
		if !strings.HasSuffix(text, ".") && !strings.HasSuffix(text, "!") && !strings.HasSuffix(text, "?") {
			text += "."
		}
		if scenes[index].Text == nil {
			scenes[index].Text = make(map[Language]string)
		}
		scenes[index].Text[req.SourceLanguage] = text
	}
	return scenes
}

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
	var bodyOrdinal atomic.Int64
	emit := func(scene Scene) error {
		scene = applyOwnedImportantPhraseHints(req, scene)
		// A no_voiceover cutaway must become fixed media BEFORE the
		// SceneTextReady boundary: the ready coordinator dispatches TTS
		// immediately, and a later batch-only pass would be too late.
		noVoiceoverScene := []Scene{scene}
		applySegmentNoVoiceoverScenes(req, noVoiceoverScene)
		scene = noVoiceoverScene[0]
		applyExplicitSegmentDuration(req, &scene)
		// ORCH-07: project the stock binding before the coordinator observes
		// the scene. A streaming worker must never see Stock == nil for a
		// scene the caller already bound to stock media.
		if !scene.ExecutionMode.IsFixedMedia() {
			applyDurableStockBindingToScene(req, &scene, int(bodyOrdinal.Add(1))-1)
		}
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
