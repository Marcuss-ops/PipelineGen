package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"go.uber.org/zap"
)

func (r *Runner) runSceneTextPhase(ctx context.Context, runID string, req contract.GenerateRequest, routing scriptpkg.ArtifactRoutingContext, exec ExecutionContext, run *GenerationRun, resumeIdx int) (*contract.GenerateResult, bool) {
	// Internal callers may construct GenerateRequest directly instead of using
	// BuildGenerateRequest. Preserve the same Drive grouping contract there.
	applyDefaultSceneRenderSubfolder(&req)
	// ── Stage 2: Generate Scene Text ─────────────────────────────
	scriptStep, startErr := r.startExecutionStep(ctx, exec, "SCRIPT", "generation")
	if startErr != nil {
		r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, startErr)
		return nil, false
	}
	var result *contract.GenerateResult
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
		hb.setOp("prepare_scene_text_generation")
		path := r.prepareSceneTextGeneration(ctx, runID, req, routing, exec)
		hb.setOp("generate_scene_text")
		scenes, generatedTrace, streamed, genErr := r.generateSceneText(ctx, runID, req, exec, path)
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
			zap.String("reason", sceneTextPathReason(req, streamed, path.topologyNeedsMaterialization, r.textGen)),
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
		var streamTranslationMetrics *contract.TranslationPipelineMetrics
		var streamAudioMetrics *contract.AudioPipelineMetrics
		if path.ready != nil {
			hb.setOp("scene_ready_wait")
			scenes, streamTranslationMetrics, streamAudioMetrics, genErr = path.ready.wait(ctx, scenes)
			if genErr != nil {
				cause := fmt.Errorf("scene ready downstream failed: %w", hb.annotate(genErr))
				r.failExecutionStepBestEffort(ctx, exec, scriptStep, cause)
				r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
				return result, false
			}
			// The normal translation/TTS stages become idempotent no-ops for
			// these scenes, while their metrics remain visible on the result.
		}
		var ok bool
		result, ok = r.assembleSceneTextResult(ctx, runID, req, exec, scriptStep, scenes,
			generatedTrace, streamTranslationMetrics, streamAudioMetrics, hb)
		if !ok {
			return result, false
		}
		if !r.finishSceneTextPhase(ctx, runID, req, exec, scriptStep, result, scenes, path, streamed, hb) {
			return result, false
		}
		r.markGenerationComplete(runID, time.Now())
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageGeneratingSceneText)))
	} else {
		var ok bool
		result, ok = r.resumeSceneTextFromCheckpoint(ctx, runID, req, exec, scriptStep, run)
		if !ok {
			return result, false
		}
	}
	return r.finalizeSceneTextPhase(ctx, runID, req, exec, scriptStep, result, scriptSkipped)
}

// assembleSceneTextResult validates the generated scenes and projects them
// into the stage result. It owns the post-processing order: explicit clip
// text, fixed sections, durable bindings, phrase grounding, output gates and
// editorial enrichment.
func (r *Runner) assembleSceneTextResult(ctx context.Context, runID string, req contract.GenerateRequest, exec ExecutionContext, scriptStep ExecutionStep, scenes []contract.Scene, generatedTrace scriptpkg.SourceTrace, streamTranslationMetrics *contract.TranslationPipelineMetrics, streamAudioMetrics *contract.AudioPipelineMetrics, hb *stageHeartbeat) (*contract.GenerateResult, bool) {
	hb.setOp("postprocess_scene_output")
	bindExplicitClipSceneText(req, scenes)
	if req.Source.Type == SourceClips {
		repairAttempts := r.repairShortClipScenes(ctx, runID, req, scenes, hb)
		if !r.validateClipSceneOutputWithAttempts(ctx, runID, req, exec, scriptStep, scenes, repairAttempts) {
			return nil, false
		}
	}
	// Literal intro/outro: injected verbatim post-LLM, never rewritten.
	// They are not part of the LLM prompt and bypass source_text.
	var fixedErr error
	scenes, fixedErr = applyFixedSections(req, scenes)
	if fixedErr != nil {
		cause := fmt.Errorf("fixed section injection failed: %w", hb.annotate(fixedErr))
		r.failExecutionStepBestEffort(ctx, exec, scriptStep, cause)
		r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
		return nil, false
	}
	applyDurableStockBindings(req, scenes)
	applySegmentClipBindings(req.ScriptParams.Segments, scenes)
	// Explicit no_voiceover segments are editorial clip cutaways: keep the
	// database clip, use its original audio, and never synthesize TTS or a
	// localized render for it. This runs before render expectations and the
	// output word budget, so the cutaway is not counted as narration.
	applySegmentNoVoiceoverScenes(req, scenes)
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
	result := &contract.GenerateResult{
		SourceLanguage:           req.SourceLanguage,
		SourceTrace:              generatedTrace,
		Render:                   req.Render,
		Output:                   output,
		WordCount:                output.WordCount,
		Scenes:                   scenes,
		Title:                    req.Title,
		OutputName:               req.OutputName,
		VoiceoverGroup:           req.ScriptParams.VoiceoverGroup,
		TranslationMetrics:       streamTranslationMetrics,
		AudioOutput:              AudioOutput{AudioMetrics: streamAudioMetrics},
		EntityImageDecisionOwner: ResolveEntityImageDecisionOwner(req.MediaPlan),
	}
	setSceneRenderExpectations(req, scenes, result)
	r.applySceneEditorialEnrichment(ctx, runID, req, result, scenes, output.Text, hb)
	return result, true
}

// finishSceneTextPhase runs the retrieval/render work that depends on the
// assembled result, then checkpoints it and emits the scene commits.
func (r *Runner) finishSceneTextPhase(ctx context.Context, runID string, req contract.GenerateRequest, exec ExecutionContext, scriptStep ExecutionStep, result *contract.GenerateResult, scenes []contract.Scene, path sceneTextGenerationPath, streamed bool, hb *stageHeartbeat) bool {
	// IMG-002: one owner per retrieval decision. When the VidRush
	// entity-image lane is active, it owns query generation, candidate
	// discovery and selection for entity images. The semantic plan must
	// exit instead of running a second retrieval/binding flow over the
	// same identities. Overlay-only plans keep the semantic fallback.
	if vidRushEntityImageOwnsDiscovery(req.MediaPlan) {
		r.log.Info("semantic search batch skipped: VidRush entity-image lane owns discovery")
	} else {
		// The plan is only useful if its exact-source ladder is executed
		// against the real provider. This phase is optional: an unwired
		// planner leaves the plan and its feedback pending, unchanged.
		hb.setOp("semantic_search_batch")
		r.executeSemanticSearchBatch(ctx, req.SourceLanguage, result, req.MediaPlan.ForceRefreshAssets)
		// Consume what the batch resolved. Only content-addressed hits bind,
		// and only where the media stage has not already chosen an asset, so
		// this can add coverage but can never overwrite verified media.
		if bound := ApplySemanticResolvedImages(result); bound > 0 {
			r.log.Info("semantic plan resolved images bound to scene entities", zap.Int("bindings", bound))
		}
	}
	// Explicit clip workflows may request real video reconstruction without
	// generating TTS. The historical fan-out was only entered after a
	// voiceover existed, which made audio.mode=NONE silently produce a script
	// and no MP4. Keep this path source-language-only and reuse the same
	// localized renderer, watermark contract, and certified result sink.
	if !r.renderNoAudioLocalizedBatch(ctx, runID, req, exec, scriptStep, result, scenes, hb) {
		return false
	}
	// Merge the certified produced videos the streaming fan-out
	// accumulated (the coordinator has no result pointer of its own) so
	// the run result records the final MP4s it rendered.
	if path.ready != nil {
		for _, rendered := range path.ready.renderedVideos() {
			applyLocalizedRenderLinkLocked(result, rendered)
			result.LocalizedRenders = append(result.LocalizedRenders, rendered)
			accumulateLocalizedRenderMetrics(result, rendered)
			// The streaming fan-out has no result pointer of its own, so its
			// parent-visible render observations are projected here, at the
			// join, instead of being lost with the coordinator.
			recordRenderStageProgress(result, rendered)
		}
		failures := path.ready.renderFailures()
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
			return false
		}
	}
	return true
}

// resumeSceneTextFromCheckpoint reloads a completed stage and replays the
// durable scenes into this attempt's SceneCommitObserver.
func (r *Runner) resumeSceneTextFromCheckpoint(ctx context.Context, runID string, req contract.GenerateRequest, exec ExecutionContext, scriptStep ExecutionStep, run *GenerationRun) (*contract.GenerateResult, bool) {
	r.log.Info("skipping completed stage", zap.String("stage", string(StageGeneratingSceneText)))
	var result *contract.GenerateResult
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
	return result, true
}

// finalizeSceneTextPhase closes the execution step, applies the nil guard and
// attaches the build identity before downstream stages read the result.
func (r *Runner) finalizeSceneTextPhase(ctx context.Context, runID string, req contract.GenerateRequest, exec ExecutionContext, scriptStep ExecutionStep, result *contract.GenerateResult, scriptSkipped bool) (*contract.GenerateResult, bool) {
	// Record resolved clip assets as script inputs once the scene plan exists.
	if result != nil && !r.attachSceneClipInputAssets(ctx, runID, exec, scriptStep, result) {
		return result, false
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
		result = &contract.GenerateResult{SourceLanguage: req.SourceLanguage, Scenes: []contract.Scene{}, Title: req.Title, OutputName: req.OutputName, VoiceoverGroup: req.ScriptParams.VoiceoverGroup, EntityImageDecisionOwner: ResolveEntityImageDecisionOwner(req.MediaPlan)}
	}
	attachBuildIdentity(result)
	return result, true
}

func (r *Runner) renderNoAudioLocalizedBatch(ctx context.Context, runID string, req contract.GenerateRequest, exec ExecutionContext, scriptStep ExecutionStep, result *contract.GenerateResult, scenes []contract.Scene, hb *stageHeartbeat) bool {
	if req.Source.Type == SourceClips && req.Render.Enabled {
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
				// Audio-bearing jobs dispatch fixed media after voiceover work
				// in the voiceover phase. Here we own fixed-media rendering only
				// for the audio-free script path.
				if req.Audio != capabilityaudio.AudioModeNone {
					continue
				}
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
								RunID: runID, ParentJobID: exec.JobID, FinalJob: req.FinalJob, SceneID: scene.ID, SceneIndex: scene.Index,
								JobID:    exec.JobID,
								Language: lang, Text: text,
								SourceLanguage: req.SourceLanguage, SourceText: sourceText,
								ClipID: clipID, ClipAssetID: clipAssetID, ClipSHA256: clipSHA256,
								ClipDurationMS: clipDurationMS, Render: sceneRenderSpec(req, scene),
								ResumeFrom: r.stagedLocalizedRender(result, scene.ID, lang, clipID),
								OnRenderReady: func(rendered contract.LocalizedRenderResult) error {
									return r.recordLocalizedRenderReady(ctx, runID, exec, result, rendered)
								},
								OnRendered: func(rendered contract.LocalizedRenderResult) error {
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
								OnFailed: func(failure contract.LocalizedRenderFailure) error {
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
			// Explicit clip segments with no narration still need a localized
			// Chronon render in mixed/voiceover jobs. Narrated scenes are handed
			// off by the TTS phase, so dispatching them here would duplicate work.
			if req.Audio != capabilityaudio.AudioModeNone && scene.ExecutionMode.AllowsTTS() {
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
							OnRenderReady: func(rendered contract.LocalizedRenderResult) error {
								return r.recordLocalizedRenderReady(ctx, runID, exec, result, rendered)
							},
							OnRendered: func(rendered contract.LocalizedRenderResult) error {
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
								if err := r.saveLocalizedRenderArtifactCheckpoint(ctx, runID, exec.Attempt, rendered); err != nil {
									return err
								}
								return nil
							},
							OnFailed: func(failure contract.LocalizedRenderFailure) error {
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
			return false
		}
		result.RenderMetrics.WallMS = time.Since(renderBatchStarted).Milliseconds()
	}
	return true
}

func applyDefaultSceneRenderSubfolder(req *contract.GenerateRequest) {
	if !req.Render.Enabled || strings.TrimSpace(req.Render.DriveSubfolderName) != "" {
		return
	}
	req.Render.DriveSubfolderName = defaultRenderSubfolderName(req.Title, req.Source.Topic, req.IdempotencyKey)
}

// setSceneRenderExpectations records the canonical render-unit promise when
// the local Chronon render lane is active.
func setSceneRenderExpectations(req contract.GenerateRequest, scenes []contract.Scene, result *contract.GenerateResult) {
	if req.Source.Type != SourceClips || !req.Render.Enabled {
		return
	}
	result.RenderMetrics = &contract.RenderMetrics{Concurrency: req.Render.RenderConcurrency}
	if sceneRenderDispatchSealed {
		return
	}
	expectedUnits := expectedRenderUnits(req, scenes)
	result.ExpectedRenderCount = expectedUnits
	result.RenderMetrics.Expected = expectedUnits
}

// applySceneEditorialEnrichment computes the optional Rust editorial products
// and applies them to the result. Failure is observable but never fatal: a
// missing NLP worker must not cost the caller the video.
func (r *Runner) applySceneEditorialEnrichment(ctx context.Context, runID string, req contract.GenerateRequest, result *contract.GenerateResult, scenes []contract.Scene, text string, hb *stageHeartbeat) {
	if _, enabled := r.sceneSemanticAnalyzer.(SceneSemanticEditorialBatchAnalyzer); !enabled || strings.TrimSpace(text) == "" {
		return
	}
	hb.setOp("editorial_enrichment")
	out, err := r.computePhraseImpactEnrichment(ctx, text, scenes, req)
	if out != nil {
		for _, tm := range out.timings {
			observePhraseImpactTimings(tm)
		}
	}
	if err != nil {
		r.log.Warn("phrase-impact analysis unavailable; continuing without extractive summary",
			zap.String("run_id", runID), zap.Error(err))
		return
	}
	if out == nil {
		return
	}
	result.Summary = out.summary
	result.BulletPoints = out.bullets
	result.HeavySentences = out.heavy
	result.ChapterManifest = out.chapterManifest
	result.SceneHighlights = out.highlights
	result.Editorial = out.editorial
	result.SemanticConsolidation = out.consolidation
	result.SemanticProductionPlan = out.productionPlan
	result.SemanticSearchFeedback = out.searchFeedback
}

func (r *Runner) attachSceneClipInputAssets(ctx context.Context, runID string, exec ExecutionContext, step ExecutionStep, result *contract.GenerateResult) bool {
	ordinal := 0
	for _, scene := range result.Scenes {
		if scene.Clip == nil {
			continue
		}
		if err := r.attachInputAsset(ctx, exec, step.StepID, scene.Clip.ID, ordinal); err != nil {
			r.failExecutionStepBestEffort(ctx, exec, step, err)
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, err)
			return false
		}
		ordinal++
	}
	return true
}

// semanticSearchBatchTimeout bounds the whole planned retrieval phase. The
// planner already bounds each query; this is the run-level ceiling that keeps a
// slow provider from consuming the render budget.
const semanticSearchBatchTimeout = 5 * time.Minute

// executeSemanticSearchBatch runs every pending composition of the plan,
// records the measured outcomes and publishes the report on the result. It is
// fail-soft: a provider outage degrades to `skip_visual` feedback (and a
// warning), never to a failed generation.
func (r *Runner) executeSemanticSearchBatch(ctx context.Context, language contract.Language, result *contract.GenerateResult, forceRefresh bool) {
	bypassCache := forceRefresh
	if r == nil || r.semanticSearchPlanner == nil || result == nil || result.SemanticProductionPlan == nil {
		return
	}
	if result.SemanticProductionPlan.SearchableUnits() == 0 {
		return
	}
	batchCtx, cancel := context.WithTimeout(ctx, semanticSearchBatchTimeout)
	defer cancel()
	report, err := r.semanticSearchPlanner.Run(batchCtx, result.SemanticProductionPlan, string(language), bypassCache)
	if report != nil {
		result.SemanticSearchReport = report
		result.SemanticSearchFeedback = report.Feedback
	}
	if err != nil {
		if r.log != nil {
			r.log.Warn("semantic search batch incomplete; planning feedback records the misses",
				zap.Error(err))
		}
		return
	}
	if r.log != nil {
		r.log.Info("semantic search batch complete",
			zap.Int("queries", report.Metrics.Queries),
			zap.Int("hits", report.Metrics.Hits),
			zap.Int("misses", report.Metrics.Misses),
			zap.Float64("hit_rate", report.Metrics.HitRate),
			zap.Int64("p50_ms", report.Metrics.P50MS),
			zap.Int64("p95_ms", report.Metrics.P95MS),
			zap.Int64("recall_ms", report.Metrics.RecallMS),
			zap.Int64("verify_ms", report.Metrics.VerifyMS),
			zap.Int64("recall_p50_ms", report.Metrics.RecallP50MS),
			zap.Int64("verify_p50_ms", report.Metrics.VerifyP50MS),
			zap.Int64("total_ms", report.Metrics.TotalMS))
	}
}
