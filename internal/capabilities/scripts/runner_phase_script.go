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
		path := r.prepareSceneTextGeneration(ctx, runID, req, routing, exec)
		ready := path.ready
		scenes, generatedTrace, streamed, genErr = r.generateSceneText(ctx, runID, req, exec, path)
		segmentTopologyNeedsMaterialization := path.topologyNeedsMaterialization
		if genErr != nil {
			cause := fmt.Errorf("generate scene text failed: %w", genErr)
			r.failExecutionStep(ctx, exec, scriptStep, cause)
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
			return result, false
		}
		// Make the scene-text path decision auditable: the streaming overlap is
		// a wall-clock claim, and a claim nobody can read from a run is not a
		// measurement. The reason names the FIRST gate that forced the batch
		// path, so "why did this run not stream?" needs no re-derivation.
		r.log.Info("scene_text_path",
			zap.String("run_id", runID),
			zap.Bool("streamed", streamed),
			zap.String("reason", sceneTextPathReason(req, streamed, segmentTopologyNeedsMaterialization, r.textGen)),
			zap.Int("scenes", len(scenes)))
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
		applyDurableStockBindings(req, scenes)
		// Caller-provided important phrases are an explicit overlay contract.
		// Keep them grounded in the final narration text even when a small/local
		// model paraphrases the brief and drops the requested literal surface.
		scenes = ensureRequestedImportantPhrases(req, scenes)
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
			SourceLanguage:     req.SourceLanguage,
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
			// scene count: a fixed intro/outro contributes one unit per bound
			// clip (one final render per clip), fanned out per render
			// language for fixed media (Intro V2).
			expectedUnits := expectedRenderUnits(req, scenes)
			result.ExpectedRenderCount = expectedUnits
			result.RenderMetrics = &RenderMetrics{Expected: expectedUnits, Concurrency: req.Render.RenderConcurrency}
		}
		if r.phraseImpactAnalyzer != nil && strings.TrimSpace(output.Text) != "" {
			chapterScenes, chapterTopics := make([]string, 0, len(scenes)), make([]string, 0, len(scenes))
			for _, scene := range scenes {
				if !scene.ExecutionMode.CountsTowardBodyWordBudget() {
					continue
				}
				text := strings.TrimSpace(scene.Text[req.SourceLanguage])
				if text == "" {
					continue
				}
				chapterSceneIndex := len(chapterScenes)
				chapterScenes = append(chapterScenes, text)
				topic := scene.ID
				for _, spec := range req.ScriptParams.Segments {
					if spec.ID != "" && spec.ID == scene.ID && strings.TrimSpace(spec.Topic) != "" {
						topic = spec.Topic
						break
					}
				}
				if topic == scene.ID && chapterSceneIndex < len(req.ScriptParams.Segments) && strings.TrimSpace(req.ScriptParams.Segments[chapterSceneIndex].Topic) != "" {
					topic = req.ScriptParams.Segments[chapterSceneIndex].Topic
				}
				chapterTopics = append(chapterTopics, topic)
			}
			if strings.Join(chapterScenes, "\n\n") != output.Text {
				chapterScenes, chapterTopics = nil, nil
			}
			// Per-scene inputs carry verified byte identity when each scene
			// text is found verbatim in the transcript; otherwise offsets
			// stay nil and that scene degrades to local-only analysis
			// (no fabricated global offsets).
			sceneInputs := buildSceneAnalysisInputs(output.Text, scenes, req)
			var impactErr error
			var rawImpact interface{} = r.phraseImpactAnalyzer
			if sceneAnalyzer, ok := rawImpact.(SceneHighlightAnalyzer); ok && len(sceneInputs) > 0 {
				impactRes, err := sceneAnalyzer.AnalyzeScenes(ctx, output.Text, string(req.SourceLanguage), sceneInputs)
				impactErr = err
				if err == nil {
					result.Summary = impactRes.Summary
					result.BulletPoints = impactRes.BulletPoints
					result.HeavySentences = impactRes.HeavySentences
					if impactRes.ChapterManifest.SchemaVersion != "" {
						manifest := impactRes.ChapterManifest
						result.ChapterManifest = &manifest
					}
					result.SceneHighlights = impactRes.SceneHighlights
					result.Editorial = buildEditorialManifest(req, sceneInputs, impactRes)
					observePhraseImpactTimings(impactRes.Timings)
				}
			} else {
				legacy, err := r.phraseImpactAnalyzer.AnalyzeWithContext(ctx, output.Text, string(req.SourceLanguage), chapterScenes, chapterTopics)
				impactErr = err
				if err == nil {
					result.Summary = legacy.Summary
					result.BulletPoints = legacy.BulletPoints
					result.HeavySentences = legacy.HeavySentences
					if legacy.ChapterManifest.SchemaVersion != "" {
						manifest := legacy.ChapterManifest
						result.ChapterManifest = &manifest
					}
					observePhraseImpactTimings(legacy.Timings)
				}
			}
			if impactErr != nil {
				// The extractive summary is an optional data product. A broken
				// or unavailable Rust NLP worker must never cost the caller the
				// video, so the failure is recorded and the run continues with
				// the editorial fields empty rather than failing closed.
				r.log.Warn("phrase-impact analysis unavailable; continuing without extractive summary",
					zap.String("run_id", runID), zap.Error(impactErr))
			}
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
				if scene.ExecutionMode.IsFixedMedia() {
					// Intro V2: fixed media fans out one render per bound
					// clip PER language, each burning its translated
					// caption. Caption text is a per-(scene, language) fact;
					// an empty caption still renders (transcript subs from
					// the asset track) but never leaks BODY narration.
					for _, lang := range fixedRenderLanguages(req, scene) {
						lang := lang
						text := fixedCaptionText(scene, req.SourceLanguage, lang)
						sourceText := strings.TrimSpace(scene.Text[req.SourceLanguage])
						if sourceText == "" {
							sourceText = text
						}
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
									DocsFolderID: routing.DocsFolderID, JobID: exec.JobID,
									Language: lang, Text: text,
									SourceLanguage: req.SourceLanguage, SourceText: sourceText,
									ClipID: clipID, ClipAssetID: clipAssetID, ClipSHA256: clipSHA256,
									ClipDurationMS: clipDurationMS, Render: sceneRenderSpec(req, scene),
									ResumeFrom: r.stagedLocalizedRender(result, scene.ID, lang, clipID),
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
					continue
				}
				// Caption text is a per-scene fact shared by all of the scene's
				// render units. Fixed-media scenes resolve it without the BODY
				// source_text fallback (display text only), so an empty
				// intro/outro never leaks narration text into its render.
				for _, lang := range renderLanguages(req, scene) {
					lang := lang
					text := localizedRenderCaptionText(req, scene)
					if scene.ExecutionMode.IsFixedMedia() {
						text = fixedCaptionText(scene, req.SourceLanguage, lang)
					}
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
								DocsFolderID: routing.DocsFolderID, JobID: exec.JobID,
								Language: lang, Text: text,
								SourceLanguage: req.SourceLanguage, SourceText: text,
								ClipID: clipID, ClipAssetID: clipAssetID, ClipSHA256: clipSHA256,
								ClipDurationMS: clipDurationMS, Render: sceneRenderSpec(req, scene),
								ResumeFrom: r.stagedLocalizedRender(result, scene.ID, lang, clipID),
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
				// The streaming fan-out has no result pointer of its own, so its
				// parent-visible render observations are projected here, at the
				// join, instead of being lost with the coordinator.
				recordRenderStageProgress(result, rendered)
			}
			failures := ready.renderFailures()
			result.LocalizedRenderFailures = append(result.LocalizedRenderFailures, failures...)
			for _, failure := range failures {
				recordRenderStageFailure(result, failure)
			}
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
		result = &GenerateResult{SourceLanguage: req.SourceLanguage, Scenes: []Scene{}, Title: req.Title, OutputName: req.OutputName, VoiceoverGroup: req.ScriptParams.VoiceoverGroup}
	}

	return result, true
}

// ── Scene-text generation path selection + dispatch ────────────────
type sceneTextGenerationPath struct {
	streamable                   bool
	topologyNeedsMaterialization bool
	ready                        *sceneReadyCoordinator
}

func (r *Runner) prepareSceneTextGeneration(ctx context.Context, runID string, req GenerateRequest, routing scriptpkg.ArtifactRoutingContext, exec ExecutionContext) sceneTextGenerationPath {
	path := sceneTextGenerationPath{
		streamable:                   SceneStreamingEligibility(req),
		topologyNeedsMaterialization: req.ScriptParams.SegmentWords > 0 && !req.ScriptParams.SingleScene && len(req.ScriptParams.Segments) == 0,
	}
	if len(req.MediaPlan.Extraction.ImportantPhrases) > 0 && !importantPhraseHintOwnersAvailable(req) {
		path.streamable = false
		path.topologyNeedsMaterialization = true
	}
	if req.Intro != nil || req.Outro != nil {
		path.streamable = false
		path.topologyNeedsMaterialization = true
	}
	if req.ScriptParams.SourceTextVerbatim {
		return path
	}
	streamEligible := !path.topologyNeedsMaterialization && (req.Source.Type != SourceClips || path.streamable)
	if streamEligible {
		if _, ok := r.textGen.(SceneTextStreamer); ok {
			path.ready = newSceneReadyCoordinator(ctx, r, runID, req, routing, exec)
		}
	}
	return path
}

func (r *Runner) generateSceneText(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, path sceneTextGenerationPath) ([]Scene, scriptpkg.SourceTrace, bool, error) {
	if req.ScriptParams.SourceTextVerbatim {
		scenes, err := materializeVerbatimSourceTextScenes(req)
		return scenes, scriptpkg.SourceTrace{}, false, err
	}

	streamEligible := !path.topologyNeedsMaterialization && (req.Source.Type != SourceClips || path.streamable)
	if streamer, ok := r.textGen.(SceneTextTraceStreamer); ok && streamEligible {
		scenes, trace, err := r.generateSceneTextStreamingWithTrace(ctx, runID, req, exec, streamer, path.ready)
		return scenes, trace, true, err
	}
	if streamer, ok := r.textGen.(SceneTextStreamer); ok && streamEligible {
		scenes, err := r.generateSceneTextStreaming(ctx, runID, req, exec, streamer, path.ready)
		return scenes, scriptpkg.SourceTrace{}, true, err
	}
	if traced, ok := r.textGen.(SceneTextTraceGenerator); ok {
		scenes, trace, err := traced.GenerateSceneTextWithTrace(ctx, req)
		return scenes, trace, false, err
	}
	scenes, err := r.textGen.GenerateSceneText(ctx, req)
	return scenes, scriptpkg.SourceTrace{}, false, err
}
