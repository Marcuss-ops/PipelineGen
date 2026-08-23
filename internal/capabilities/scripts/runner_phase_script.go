package scriptgeneration

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// Clip-backed narrator intros are intentionally short-form. Keep a small
// floor to reject empty/placeholders without imposing long-form documentary
// minimums on an 18-word target scene.
const minimumClipSceneWords = 12

// outputFromScenes builds the single durable narration projection from the
// ordered scene list. The requested source language wins; a first available
// language is only a compatibility fallback for older test generators.
func outputFromScenes(scenes []Scene, language Language) GenerateOutput {
	parts := make([]string, 0, len(scenes))
	for _, scene := range scenes {
		text := strings.TrimSpace(scene.Text[language])
		if text == "" {
			for _, candidate := range scene.Text {
				if strings.TrimSpace(candidate) != "" {
					text = strings.TrimSpace(candidate)
					break
				}
			}
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	text := strings.Join(parts, "\n\n")
	return GenerateOutput{Text: text, WordCount: len(strings.Fields(text))}
}

func minimumGeneratedWords(req GenerateRequest) int {
	if req.ScriptParams.MinWords > 0 {
		return req.ScriptParams.MinWords
	}
	return 1
}

// bindExplicitClipSceneText preserves the caller's one-clip/one-scene
// contract. When a clips source supplies explicit SCENE N lines, the model
// may embellish each line but it must not collapse all narration into scene 1
// or leave later clip scenes empty. The binding is intentionally limited to
// the explicit marker format so free-form source text keeps its existing
// generation behavior.
func bindExplicitClipSceneText(req GenerateRequest, scenes []Scene) {
	if req.Source.Type != SourceClips || len(req.Source.ClipIDs) == 0 || len(scenes) != len(req.Source.ClipIDs) {
		return
	}
	lines := make([]string, 0, len(scenes))
	for _, line := range strings.Split(req.Source.SourceText, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 8 || !strings.EqualFold(line[:5], "scene") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 || strings.TrimSpace(line[colon+1:]) == "" {
			return
		}
		lines = append(lines, strings.TrimSpace(line[colon+1:]))
	}
	if len(lines) != len(scenes) {
		return
	}
	for i := range scenes {
		if scenes[i].Text == nil {
			scenes[i].Text = make(map[Language]string)
		}
		// The per-clip source line is evidence/instructions, not the final
		// narration. Preserve a non-empty model answer; only use the supplied
		// line as a recovery value when generation left the scene empty.
		if strings.TrimSpace(scenes[i].Text[req.SourceLanguage]) == "" {
			scenes[i].Text[req.SourceLanguage] = lines[i]
		}
	}
}

// hasExplicitSceneMarkers returns true when sourceText contains "SCENE N:"
// markers that bindExplicitClipSceneText would use for post-generation
// rebinding. Streaming must be disabled when markers are present because
// scene text emitted scene-by-scene could be overwritten by the bind step.
func hasExplicitSceneMarkers(sourceText string) bool {
	for _, line := range strings.Split(sourceText, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 8 || !strings.EqualFold(line[:5], "scene") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon >= 0 && strings.TrimSpace(line[colon+1:]) != "" {
			return true
		}
	}
	return false
}

// SceneStreamingEligibility determines whether a SourceClips request can
// safely use per-scene streaming (SceneTextReady fired scene-by-scene as
// each scene's text becomes final) instead of the barrier batch path.
//
// Streaming is safe when NO post-generation mutation will change scene
// text — the bindExplicitClipSceneText step is the only mutation, and it
// fires ONLY when SCENE N: markers are present in the source text. When
// markers are absent, the source text is already the definitive text OR
// the LLM generates fresh text from clip evidence alone.
//
// Eligibility conditions:
//
//  1. Source is SourceClips with at least 1 ClipID
//  2. Source text has NO explicit "SCENE N:" markers
//  3. Optional extra safety: ScriptParams.Segments are present (1:1 stable
//     clip→scene mapping); when absent the generator will emit a scene per
//     clip anyway, but explicit segments make the contract explicit.
//
// The "SCENE N:" marker check is the canonical signal: if present,
// bindExplicitClipSceneText WILL fire and could overwrite already-emitted
// scene text, corrupting downstream consumers (NLP, TTS, render) that
// already started on stale text.
func SceneStreamingEligibility(req GenerateRequest) bool {
	if req.Source.Type != SourceClips || len(req.Source.ClipIDs) == 0 {
		return false
	}
	// SCENE N: markers → bindExplicitClipSceneText will fire → NOT streamable.
	if hasExplicitSceneMarkers(req.Source.SourceText) {
		return false
	}
	// Explicit Segments with 1:1 clip mapping strengthen the safe streaming
	// contract but are not mandatory: without them the generator still emits
	// one scene per clip. The marker check alone is the safety gate.
	return true
}

func validateMinimumGeneratedOutput(req GenerateRequest, output GenerateOutput) error {
	actual := len(strings.Fields(strings.TrimSpace(output.Text)))
	minimum := minimumGeneratedWords(req)
	if actual == 0 || actual < minimum {
		return fmt.Errorf("%w: actual_words=%d minimum_words=%d", ErrMinimumTextGate, actual, minimum)
	}
	return nil
}

func contaminatedClipNarration(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, marker := range []string{"clip description:", "write a new", "do not copy the description", "source text:"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func (r *Runner) runSceneTextPhase(ctx context.Context, runID string, req GenerateRequest, routing kernelscript.ArtifactRoutingContext, exec ExecutionContext, run *GenerationRun, resumeIdx int) (*GenerateResult, bool) {
	// ── Stage 2: Generate Scene Text ─────────────────────────────
	scriptStep, startErr := r.startExecutionStep(ctx, exec, "SCRIPT", "generation")
	if startErr != nil {
		r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, startErr)
		return nil, false
	}
	var result *GenerateResult
	scriptSkipped := stageSkipped(resumeIdx, StageGeneratingSceneText)
	if !scriptSkipped {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageGeneratingSceneText); err != nil {
			r.failExecutionStep(ctx, exec, scriptStep, err)
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, err)
			return result, false
		}
		r.markGenerationStart(runID, time.Now())
		var scenes []Scene
		var genErr error
		var generatedTrace scriptpkg.SourceTrace
		streamed := false
		var streamTranslationMetrics *TranslationPipelineMetrics
		var streamAudioMetrics *AudioPipelineMetrics
		var ready *sceneReadyCoordinator
		// ── Streaming eligibility ──────────────────────────────────
		// SceneTextStreamer can emit scenes one-by-one so downstream
		// branches (NLP, TTS, render) start before the LLM finishes.
		// Historically this was disabled for all SourceClips because
		// bindExplicitClipSceneText can mutate scene text after
		// generation. SceneStreamingEligibility now gates streaming
		// per-request: clips with no SCENE N: markers in source text
		// are streamable (no post-gen rebinding).
		streamable := SceneStreamingEligibility(req)
		if _, ok := r.textGen.(SceneTextStreamer); ok && (req.Source.Type != SourceClips || streamable) {
			ready = newSceneReadyCoordinator(ctx, r, runID, req, routing, exec)
		}
		if streamer, ok := r.textGen.(SceneTextTraceStreamer); ok && (req.Source.Type != SourceClips || streamable) {
			streamed = true
			scenes, generatedTrace, genErr = r.generateSceneTextStreamingWithTrace(ctx, runID, req, exec, streamer, ready)
		} else if streamer, ok := r.textGen.(SceneTextStreamer); ok && (req.Source.Type != SourceClips || streamable) {
			// Scene-ready streaming: emit SceneTextReady(N) per scene
			// as its text becomes final so downstream branches start
			// while the LLM keeps generating later scenes.
			streamed = true
			scenes, genErr = r.generateSceneTextStreaming(ctx, runID, req, exec, streamer, ready)
		} else if traced, ok := r.textGen.(SceneTextTraceGenerator); ok {
			scenes, generatedTrace, genErr = traced.GenerateSceneTextWithTrace(ctx, req)
		} else {
			scenes, genErr = r.textGen.GenerateSceneText(ctx, req)
		}
		if genErr != nil {
			cause := fmt.Errorf("generate scene text failed: %w", genErr)
			r.failExecutionStep(ctx, exec, scriptStep, cause)
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
			return result, false
		}
		if len(scenes) == 0 {
			cause := fmt.Errorf("generate scene text returned zero scenes")
			r.failExecutionStep(ctx, exec, scriptStep, cause)
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
			return result, false
		}
		if ready != nil {
			scenes, streamTranslationMetrics, streamAudioMetrics, genErr = ready.wait(ctx, scenes)
			if genErr != nil {
				cause := fmt.Errorf("scene ready downstream failed: %w", genErr)
				r.failExecutionStep(ctx, exec, scriptStep, cause)
				r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
				return result, false
			}
			// The normal translation/TTS stages become idempotent no-ops for
			// these scenes, while their metrics remain visible on the result.
		}
		bindExplicitClipSceneText(req, scenes)
		if req.Source.Type == SourceClips {
			for i, scene := range scenes {
				text := strings.TrimSpace(scene.Text[req.SourceLanguage])
				words := len(strings.Fields(text))
				lower := strings.ToLower(text)
				placeholder := text == "" || words < minimumClipSceneWords || lower == fmt.Sprintf("scene %d", i+1) || lower == "the"
				if placeholder || contaminatedClipNarration(text) {
					code := "SCRIPT_SCENE_TEXT_INVALID"
					if contaminatedClipNarration(text) {
						code = "SCRIPT_SCENE_TEXT_CONTAMINATED"
					}
					cause := fmt.Errorf("%s: scene=%d words=%d minimum=%d placeholder=%t", code, i, words, minimumClipSceneWords, lower == fmt.Sprintf("scene %d", i+1) || lower == "the")
					r.failExecutionStep(ctx, exec, scriptStep, cause)
					r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
					return result, false
				}
			}
		}
		output := outputFromScenes(scenes, req.SourceLanguage)
		if gateErr := validateMinimumGeneratedOutput(req, output); gateErr != nil {
			cause := fmt.Errorf("minimum generated text gate: %w", gateErr)
			r.failExecutionStep(ctx, exec, scriptStep, cause)
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
			return nil, false
		}
		result = &GenerateResult{
			SourceTrace:        generatedTrace,
			Render:             req.Render,
			Output:             output,
			WordCount:          output.WordCount,
			Scenes:             scenes,
			Title:              req.Title,
			OutputName:         req.OutputName,
			VoiceoverGroup:     req.ScriptParams.VoiceoverGroup,
			TranslationMetrics: streamTranslationMetrics,
			AudioMetrics:       streamAudioMetrics,
		}
		if req.Source.Type == SourceClips && req.Render.Enabled {
			result.ExpectedRenderCount = len(scenes)
			result.FinalVideoRequired = req.Render.AssembleFinal
			result.RenderMetrics = &RenderMetrics{Expected: len(scenes), Concurrency: 1}
		}
		// Explicit clip workflows may request real video reconstruction without
		// generating TTS. The historical fan-out was only entered after a
		// voiceover existed, which made audio.mode=NONE silently produce a script
		// and no MP4. Keep this path source-language-only and reuse the same
		// localized renderer, watermark contract, and certified result sink.
		if req.Source.Type == SourceClips && req.Render.Enabled && req.Audio == capabilityaudio.AudioModeNone {
			renderBatchStarted := time.Now()
			for _, scene := range scenes {
				renderStarted := time.Now()
				clipID, clipAssetID, clipSHA256, clipDurationMS := localizedRenderClipFields(scene)
				text := strings.TrimSpace(scene.Text[req.SourceLanguage])
				if text == "" {
					text = strings.TrimSpace(req.Source.SourceText)
				}
				if err := r.enqueueLocalizedRender(ctx, LocalizedRenderInput{
					RunID: runID, ParentJobID: exec.JobID, SceneID: scene.ID, SceneIndex: scene.Index,
					Language: req.SourceLanguage, Text: text,
					SourceLanguage: req.SourceLanguage, SourceText: text,
					ClipID: clipID, ClipAssetID: clipAssetID, ClipSHA256: clipSHA256,
					ClipDurationMS: clipDurationMS, Render: req.Render,
					OnRendered: func(rendered LocalizedRenderResult) error {
						r.localizedRenderMu.Lock()
						applyLocalizedRenderLinkLocked(result, rendered)
						result.LocalizedRenders = append(result.LocalizedRenders, rendered)
						result.RenderMetrics.Successful = len(result.LocalizedRenders)
						accumulateLocalizedRenderMetrics(result, rendered)
						r.localizedRenderMu.Unlock()
						if rendered.WallMS == 0 {
							result.RenderMetrics.WorkMS += time.Since(renderStarted).Milliseconds()
						}
						return nil
					},
					OnFailed: func(failure LocalizedRenderFailure) error {
						result.LocalizedRenderFailures = append(result.LocalizedRenderFailures, failure)
						result.RenderMetrics.Failed = len(result.LocalizedRenderFailures)
						result.RenderMetrics.RenderMS += time.Since(renderStarted).Milliseconds()
						upper := strings.ToUpper(failure.Error)
						if strings.Contains(upper, "CUDA") || strings.Contains(upper, "OUT OF MEMORY") {
							result.RenderMetrics.GPUOOMs++
						}
						return nil
					},
				}); err != nil {
					cause := fmt.Errorf("enqueue no-audio localized render: %w", err)
					r.failExecutionStep(ctx, exec, scriptStep, cause)
					r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
					return result, false
				}
			}
			result.RenderMetrics.WallMS = time.Since(renderBatchStarted).Milliseconds()
		}
		// Merge the certified produced videos the streaming fan-out
		// accumulated (the coordinator has no result pointer of its own) so
		// the run result records the final MP4s it rendered.
		if ready != nil {
			for _, rendered := range ready.renderedVideos() {
				applyLocalizedRenderLinkLocked(result, rendered)
				result.LocalizedRenders = append(result.LocalizedRenders, rendered)
				accumulateLocalizedRenderMetrics(result, rendered)
			}
			result.LocalizedRenderFailures = append(result.LocalizedRenderFailures, ready.renderFailures()...)
		}
		r.checkpoint(ctx, runID, result)
		if !streamed {
			var emitErr error
			kernobs.MeasureStage(ctx, "emit_scene_commits", func(stageCtx context.Context) error {
				emitErr = r.emitSceneCommits(stageCtx, runID, req, exec, scenes)
				return emitErr
			})
			if emitErr != nil {
				cause := fmt.Errorf("emit scene commits: %w", emitErr)
				r.failExecutionStep(ctx, exec, scriptStep, cause)
				r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
				return result, false
			}
		}
		r.markGenerationComplete(runID, time.Now())
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageGeneratingSceneText)))
	} else {
		r.log.Info("skipping completed stage", zap.String("stage", string(StageGeneratingSceneText)))
		// Load result from repo if available.
		if run != nil && run.Result != nil {
			result = run.Result
		}
	}

	// Record resolved clip assets as script inputs once the scene plan exists.
	if result != nil {
		ordinal := 0
		for _, scene := range result.Scenes {
			if scene.Clip != nil {
				if err := r.attachInputAsset(ctx, exec, scriptStep.StepID, scene.Clip.ID, ordinal); err != nil {
					r.failExecutionStep(ctx, exec, scriptStep, err)
					r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, err)
					return result, false
				}
				ordinal++
			}
		}
	}
	if scriptSkipped {
		if err := r.skipExecutionStep(ctx, exec, scriptStep); err != nil {
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, err)
			return result, false
		}
	} else if err := r.completeExecutionStep(ctx, exec, scriptStep); err != nil {
		r.failExecutionStep(ctx, exec, scriptStep, err)
		r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, err)
		return result, false
	}

	// Nil guard: result must be non-nil before downstream stages.
	if result == nil {
		result = &GenerateResult{Scenes: []Scene{}, Title: req.Title, OutputName: req.OutputName, VoiceoverGroup: req.ScriptParams.VoiceoverGroup}
	}

	return result, true
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
