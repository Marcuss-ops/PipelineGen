package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"go.uber.org/zap"
)

// Clip-backed narrator intros are intentionally short-form. Keep a small
// floor to reject empty/placeholders without imposing long-form documentary
// minimums on an 18-word target scene.
const minimumClipSceneWords = 12

// outputFromScenes builds the single durable BODY narration projection from
// the ordered scene list. The requested source language wins; a first
// available language is only a compatibility fallback for older test
// generators. Protected fixed-media scenes are deliberately excluded: their
// DisplayText/text belongs to the timeline/document surface, never to the
// generated BODY word budget or minimum-word gate.
func outputFromScenes(scenes []Scene, language Language) GenerateOutput {
	parts := make([]string, 0, len(scenes))
	fallbackUsed := false
	for _, scene := range scenes {
		if !scene.ExecutionMode.CountsTowardBodyWordBudget() {
			continue
		}
		text := strings.TrimSpace(scene.Text[language])
		if text == "" {
			// Deterministic fallback: pick lexicographically smallest lang key
			// instead of random Go map iteration, so output is stable across runs.
			bestLang := ""
			for l, candidate := range scene.Text {
				if strings.TrimSpace(candidate) == "" {
					continue
				}
				if bestLang == "" || string(l) < bestLang {
					bestLang = string(l)
					text = strings.TrimSpace(candidate)
				}
			}
			if text != "" {
				fallbackUsed = true
			}
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	text := strings.Join(parts, "\n\n")
	return GenerateOutput{
		Text:                       text,
		WordCount:                  len(strings.Fields(text)),
		SourceLanguageFallbackUsed: fallbackUsed,
	}
}

// minimumGeneratedWords returns the minimum number of generated BODY words.
// It never includes DisplayText or narration attached to fixed-media scenes;
// those scenes are excluded by outputFromScenes before this gate runs.
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

// validateMinimumGeneratedOutput enforces the minimum generated BODY word
// count. GenerateOutput.Text is the BODY-only projection; fixed-media
// DisplayText and legacy fixed narration are never part of `actual`.
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

func (r *Runner) runSceneTextPhase(ctx context.Context, runID string, req GenerateRequest, routing scriptpkg.ArtifactRoutingContext, exec ExecutionContext, run *GenerationRun, resumeIdx int) (*GenerateResult, bool) {
	// Internal callers may construct GenerateRequest directly instead of using
	// BuildGenerateRequest. Preserve the same Drive grouping contract there.
	if req.Render.Enabled && strings.TrimSpace(req.Render.DriveSubfolderName) == "" {
		req.Render.DriveSubfolderName = strings.TrimSpace(req.Title)
		if req.Render.DriveSubfolderName == "" {
			req.Render.DriveSubfolderName = strings.TrimSpace(req.Source.Topic)
		}
	}
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
		// Literal intro/outro must not be streamed scene-by-scene: they are
		// injected verbatim post-LLM and never rewritten from source_text.
		// Force batch when a fixed section is present so SceneTextReady
		// events are emitted only after injection.
		if req.Intro != nil || req.Outro != nil {
			streamable = false
		}
		// A declared segment budget requires whole-prose materialization before
		// SceneCommitted; streaming a model's provisional single scene would
		// permanently launch VidRush enrichment with the wrong topology.
		segmentTopologyNeedsMaterialization := req.ScriptParams.SegmentWords > 0 && !req.ScriptParams.SingleScene && len(req.ScriptParams.Segments) == 0
		// An explicit segment plan is also authoritative. Streaming a model
		// response here can emit one opaque scene before the batch path has a
		// chance to materialize the declared topology, which would collapse
		// every downstream VidRush commit onto scene-0. Commit only the
		// materialized scenes for explicit segment plans.
		if len(req.ScriptParams.Segments) > 0 {
			segmentTopologyNeedsMaterialization = true
		}
		if req.Intro != nil || req.Outro != nil {
			segmentTopologyNeedsMaterialization = true
		}
		if _, ok := r.textGen.(SceneTextStreamer); ok && !segmentTopologyNeedsMaterialization && (req.Source.Type != SourceClips || streamable) {
			ready = newSceneReadyCoordinator(ctx, r, runID, req, routing, exec)
		}
		if streamer, ok := r.textGen.(SceneTextTraceStreamer); ok && !segmentTopologyNeedsMaterialization && (req.Source.Type != SourceClips || streamable) {
			streamed = true
			scenes, generatedTrace, genErr = r.generateSceneTextStreamingWithTrace(ctx, runID, req, exec, streamer, ready)
		} else if streamer, ok := r.textGen.(SceneTextStreamer); ok && !segmentTopologyNeedsMaterialization && (req.Source.Type != SourceClips || streamable) {
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
		// Small/local models commonly return one opaque prose scene even when
		// the request declares a per-segment budget. The batch postprocessor
		// already materializes that shape, but the incremental VidRush path
		// commits scenes before postprocessors run. Normalize here so keyword
		// extraction and provider fan-out receive the same segment topology.
		if !streamed {
			scenes = materializeGeneratedScenes(req, scenes)
		}
		if req.ScriptParams.SegmentWords > 0 && !req.ScriptParams.SingleScene {
			normalizeGeneratedSceneIdentity(scenes)
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
		if req.Source.Type == SourceClips && !r.validateClipSceneOutput(ctx, runID, req, exec, scriptStep, scenes) {
			return result, false
		}
		// Literal intro/outro: injected verbatim post-LLM, never rewritten.
		// They are not part of the LLM prompt and bypass source_text.
		var fixedErr error
		scenes, fixedErr = applyFixedSections(req, scenes)
		if fixedErr != nil {
			cause := fmt.Errorf("fixed section injection failed: %w", fixedErr)
			r.failExecutionStep(ctx, exec, scriptStep, cause)
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
			return result, false
		}
		output := outputFromScenes(scenes, req.SourceLanguage)
		if output.SourceLanguageFallbackUsed {
			// A masked translation bug must be observable: the body is in the
			// wrong language but the word-count gate cannot detect it.
			observability.ScriptFallbackUsedTotal.WithLabelValues("source_language_missing").Inc()
			r.log.Warn("script body fell back to a non-requested scene language",
				zap.String("run_id", runID),
				zap.String("requested_language", string(req.SourceLanguage)))
		}
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
			// Expected renders are the canonical render-unit count, not the
			// scene count: a fixed intro/outro bound to two clips contributes
			// two units (one final render per clip).
			expectedUnits := RenderUnitCount(scenes)
			result.ExpectedRenderCount = expectedUnits
			result.RenderMetrics = &RenderMetrics{Expected: expectedUnits, Concurrency: req.Render.RenderConcurrency}
		}
		// Explicit clip workflows may request real video reconstruction without
		// generating TTS. The historical fan-out was only entered after a
		// voiceover existed, which made audio.mode=NONE silently produce a script
		// and no MP4. Keep this path source-language-only and reuse the same
		// localized renderer, watermark contract, and certified result sink.
		if req.Source.Type == SourceClips && req.Render.Enabled && req.Audio == capabilityaudio.AudioModeNone {
			renderBatchStarted := time.Now()
			concurrency := req.Render.RenderConcurrency
			if concurrency < 1 {
				concurrency = 2
			}
			sem := make(chan struct{}, concurrency)
			var renders sync.WaitGroup
			var renderErr error
			var renderErrMu sync.Mutex
			for _, scene := range scenes {
				scene := scene
				// Caption text is a per-scene fact shared by all of the scene's
				// render units. Fixed-media scenes resolve it without the BODY
				// source_text fallback (display text only), so an empty
				// intro/outro never leaks narration text into its render.
				text := localizedRenderCaptionText(req, scene)
				for _, unit := range RenderUnitsForScene(scene) {
					unit := unit
					clipID, clipAssetID, clipSHA256, clipDurationMS := localizedRenderUnitClipFields(unit)
					renders.Add(1)
					go func() {
						defer renders.Done()
						sem <- struct{}{}
						defer func() { <-sem }()
						renderStarted := time.Now()
						if err := r.enqueueLocalizedRender(ctx, LocalizedRenderInput{
							RunID: runID, ParentJobID: exec.JobID, SceneID: scene.ID, SceneIndex: scene.Index,
							Language: req.SourceLanguage, Text: text,
							SourceLanguage: req.SourceLanguage, SourceText: text,
							ClipID: clipID, ClipAssetID: clipAssetID, ClipSHA256: clipSHA256,
							ClipDurationMS: clipDurationMS, Render: req.Render,
							ResumeFrom: r.stagedLocalizedRender(result, scene.ID, req.SourceLanguage, clipID),
							OnRenderReady: func(rendered LocalizedRenderResult) error {
								return r.recordLocalizedRenderReady(ctx, exec, result, rendered)
							},
							OnRendered: func(rendered LocalizedRenderResult) error {
								r.localizedRenderMu.Lock()
								applyLocalizedRenderLinkLocked(result, rendered)
								result.LocalizedRenders = append(result.LocalizedRenders, rendered)
								result.RenderMetrics.Successful = len(result.LocalizedRenders)
								accumulateLocalizedRenderMetrics(result, rendered)
								r.localizedRenderMu.Unlock()
								if rendered.WallMS == 0 {
									r.localizedRenderMu.Lock()
									result.RenderMetrics.WorkMS += time.Since(renderStarted).Milliseconds()
									r.localizedRenderMu.Unlock()
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
							renderErrMu.Lock()
							if renderErr == nil {
								renderErr = fmt.Errorf("enqueue no-audio localized render: %w", err)
							}
							renderErrMu.Unlock()
						}
					}()
				}
			}
			renders.Wait()
			if renderErr != nil {
				r.failExecutionStep(ctx, exec, scriptStep, renderErr)
				r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, renderErr)
				return result, false
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
