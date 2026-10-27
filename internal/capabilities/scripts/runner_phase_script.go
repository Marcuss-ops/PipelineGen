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
			r.failExecutionStepBestEffort(ctx, exec, scriptStep, err)
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, err)
			return result, false
		}
		r.markGenerationStart(runID, time.Now())
		hb := r.startStageHeartbeat(runID, StageGeneratingSceneText)
		defer hb.close()
		var scenes []Scene
		var genErr error
		var generatedTrace scriptpkg.SourceTrace
		streamed := false
		var streamTranslationMetrics *TranslationPipelineMetrics
		var streamAudioMetrics *AudioPipelineMetrics
		hb.setOp("prepare_scene_text_generation")
		path := r.prepareSceneTextGeneration(ctx, runID, req, routing, exec)
		ready := path.ready
		hb.setOp("generate_scene_text")
		scenes, generatedTrace, streamed, genErr = r.generateSceneText(ctx, runID, req, exec, path)
		segmentTopologyNeedsMaterialization := path.topologyNeedsMaterialization
		if genErr != nil {
			cause := fmt.Errorf("generate scene text failed: %w", hb.annotate(genErr))
			r.failExecutionStepBestEffort(ctx, exec, scriptStep, cause)
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
			cause := hb.annotate(fmt.Errorf("generate scene text returned zero scenes"))
			r.failExecutionStepBestEffort(ctx, exec, scriptStep, cause)
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
			return result, false
		}
		if ready != nil {
			hb.setOp("scene_ready_wait")
			scenes, streamTranslationMetrics, streamAudioMetrics, genErr = ready.wait(ctx, scenes)
			if genErr != nil {
				cause := fmt.Errorf("scene ready downstream failed: %w", hb.annotate(genErr))
				r.failExecutionStepBestEffort(ctx, exec, scriptStep, cause)
				r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
				return result, false
			}
			// The normal translation/TTS stages become idempotent no-ops for
			// these scenes, while their metrics remain visible on the result.
		}
		hb.setOp("postprocess_scene_output")
		bindExplicitClipSceneText(req, scenes)
		if req.Source.Type == SourceClips && !r.validateClipSceneOutput(ctx, runID, req, exec, scriptStep, scenes) {
			return result, false
		}
		// Literal intro/outro: injected verbatim post-LLM, never rewritten.
		// They are not part of the LLM prompt and bypass source_text.
		var fixedErr error
		scenes, fixedErr = applyFixedSections(req, scenes)
		if fixedErr != nil {
			cause := fmt.Errorf("fixed section injection failed: %w", hb.annotate(fixedErr))
			r.failExecutionStepBestEffort(ctx, exec, scriptStep, cause)
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
			cause := fmt.Errorf("minimum generated text gate: %w", hb.annotate(gateErr))
			r.failExecutionStepBestEffort(ctx, exec, scriptStep, cause)
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
		// The metrics struct exists whenever a render-enabled clip run can
		// record render work; the EXPECTATION (the completion gate in
		// completeRun) is only promised while dispatch is live. Sealing
		// ExpectedRenderCount under sceneRenderDispatchSealed (runner_deps.go)
		// keeps the run from demanding work this runner never dispatches.
		if req.Source.Type == SourceClips && req.Render.Enabled {
			result.RenderMetrics = &RenderMetrics{Concurrency: req.Render.RenderConcurrency}
			if !sceneRenderDispatchSealed {
				// Expected renders are the canonical render-unit count, not the
				// scene count: a fixed intro/outro contributes one unit per bound
				// clip (one final render per clip), fanned out per render
				// language for fixed media (Intro V2).
				expectedUnits := expectedRenderUnits(req, scenes)
				result.ExpectedRenderCount = expectedUnits
				result.RenderMetrics.Expected = expectedUnits
			}
		}
		// The unified Rust editorial enrichment computes VALUES only over the
		// stable text; the single writer below owns every result mutation.
		_, unifiedEditorialEnabled := r.sceneSemanticAnalyzer.(SceneSemanticEditorialBatchAnalyzer)
		piEnabled := unifiedEditorialEnabled && strings.TrimSpace(output.Text) != ""
		type piOutcome struct {
			summary         string
			bullets         []string
			heavy           []scriptpkg.ImportantSentence
			chapterManifest *scriptpkg.ChapterManifest
			highlights      []scriptpkg.SceneHighlight
			editorial       *scriptpkg.EditorialManifest
			timings         []PhraseImpactTimings
			consolidation   *SemanticVisualConsolidation
			productionPlan  *SemanticProductionPlan
			searchFeedback  *SemanticSearchFeedback
			err             error
		}
		var piOut piOutcome
		if piEnabled {
			hb.setOp("editorial_enrichment")
			out, err := r.computePhraseImpactEnrichment(ctx, output.Text, scenes, req)
			if err != nil {
				piOut.err = err
			} else if out != nil {
				piOut.summary = out.summary
				piOut.bullets = out.bullets
				piOut.heavy = out.heavy
				piOut.chapterManifest = out.chapterManifest
				piOut.highlights = out.highlights
				piOut.editorial = out.editorial
				piOut.timings = out.timings
				piOut.consolidation = out.consolidation
				piOut.productionPlan = out.productionPlan
				piOut.searchFeedback = out.searchFeedback
			}
		}
		if piEnabled {
			for _, tm := range piOut.timings {
				observePhraseImpactTimings(tm)
			}
			if piOut.err != nil {
				// The extractive summary is an optional data product. A broken
				// or unavailable Rust NLP worker must never cost the caller the
				// video, so the failure is recorded and the run continues with
				// the editorial fields empty rather than failing closed.
				r.log.Warn("phrase-impact analysis unavailable; continuing without extractive summary",
					zap.String("run_id", runID), zap.Error(piOut.err))
			} else {
				result.Summary = piOut.summary
				result.BulletPoints = piOut.bullets
				result.HeavySentences = piOut.heavy
				result.ChapterManifest = piOut.chapterManifest
				result.SceneHighlights = piOut.highlights
				result.Editorial = piOut.editorial
				result.SemanticConsolidation = piOut.consolidation
				result.SemanticProductionPlan = piOut.productionPlan
				result.SemanticSearchFeedback = piOut.searchFeedback
			}
		}
		// The plan is only useful if its exact-source ladder is executed
		// against the real provider. This phase is optional: an unwired
		// planner leaves the plan and its feedback pending, unchanged.
		hb.setOp("semantic_search_batch")
		r.executeSemanticSearchBatch(ctx, req.SourceLanguage, result)
		// Consume what the batch resolved. Only content-addressed hits bind,
		// and only where the media stage has not already chosen an asset, so
		// this can add coverage but can never overwrite verified media.
		if bound := ApplySemanticResolvedImages(result); bound > 0 {
			r.log.Info("semantic plan resolved images bound to scene entities", zap.Int("bindings", bound))
		}
		// Explicit clip workflows may request real video reconstruction without
		// generating TTS. The historical fan-out was only entered after a
		// voiceover existed, which made audio.mode=NONE silently produce a script
		// and no MP4. Keep this path source-language-only and reuse the same
		// localized renderer, watermark contract, and certified result sink.
		if req.Source.Type == SourceClips && req.Render.Enabled && req.Audio == capabilityaudio.AudioModeNone {
			hb.setOp("localized_render_fanout")
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
									JobID:    exec.JobID,
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
								JobID:    exec.JobID,
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
				renderErr = hb.annotate(renderErr)
				r.failExecutionStepBestEffort(ctx, exec, scriptStep, renderErr)
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
			hb.setOp("emit_scene_commits")
			var emitErr error
			_ = kernobs.MeasureStage(ctx, "emit_scene_commits", func(stageCtx context.Context) error {
				emitErr = r.emitSceneCommits(stageCtx, runID, req, exec, scenes)
				return emitErr
			})
			if emitErr != nil {
				cause := fmt.Errorf("emit scene commits: %w", hb.annotate(emitErr))
				r.failExecutionStepBestEffort(ctx, exec, scriptStep, cause)
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
		// SceneCommitObserver is attempt-scoped (the VidRush coordinator is
		// rebuilt for each Execute). A voiceover or later-stage retry resumes
		// past scene generation, so replay the durable scenes into this attempt's
		// fresh coordinator before its final VidRush barrier. Without this, the
		// barrier sees an empty result and MediaCert reports every check failed.
		if result != nil {
			if err := r.emitSceneCommits(ctx, runID, req, exec, result.Scenes); err != nil {
				cause := fmt.Errorf("replay scene commits from checkpoint: %w", err)
				r.failExecutionStepBestEffort(ctx, exec, scriptStep, cause)
				r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
				return result, false
			}
		}
	}

	// Record resolved clip assets as script inputs once the scene plan exists.
	if result != nil {
		ordinal := 0
		for _, scene := range result.Scenes {
			if scene.Clip != nil {
				if err := r.attachInputAsset(ctx, exec, scriptStep.StepID, scene.Clip.ID, ordinal); err != nil {
					r.failExecutionStepBestEffort(ctx, exec, scriptStep, err)
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
		r.failExecutionStepBestEffort(ctx, exec, scriptStep, err)
		r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, err)
		return result, false
	}

	// Nil guard: result must be non-nil before downstream stages.
	if result == nil {
		result = &GenerateResult{SourceLanguage: req.SourceLanguage, Scenes: []Scene{}, Title: req.Title, OutputName: req.OutputName, VoiceoverGroup: req.ScriptParams.VoiceoverGroup}
	}

	return result, true
}
