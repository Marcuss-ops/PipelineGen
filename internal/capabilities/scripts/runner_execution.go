// Package scriptgeneration — runner_execution.go owns the run-scoped
// execution wrapper and the explicit phase decomposition of the durable
// workflow. The public entry point (ExecuteWithContext) stays intentionally
// small; the ordering and every fail/checkpoint/return path live here as
// named business phases so each phase has exactly one semantic reason to be
// complex. All cross-cutting concerns that used to be repeated inline —
// phase timing/metrics, checkpointing, error classification/termination,
// logging — are owned ONCE by the wrapper instead of by every phase.
package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

// executionRun bundles all mutable run-scoped workflow state behind one
// wrapper so phase methods share a single source of truth and the common
// concern wrappers (measure, checkpoint, fail, log) are declared exactly
// once. It deliberately carries values — not a generic engine — so each
// phase stays an explicit named step in runExecution().
type executionRun struct {
	r     *Runner
	ctx   context.Context
	runID string
	req   GenerateRequest
	exec  ExecutionContext

	// routing is the canonical artifact routing context resolved ONCE at
	// run start (godlike/06 SSOT). Downstream phases consume it and never
	// re-derive Project/Language/folder routing.
	routing scriptpkg.ArtifactRoutingContext

	// run/resumeIdx capture the durable resume-from-checkpoint state.
	run       *GenerationRun
	resumeIdx int

	// coordinator is this run's VidRush coordinator wiring (nil when the
	// run has no VidRush pipeline). It is registered in beginVidRush and
	// released via defer once the run's phases completed or failed.
	coordinator *VidRushIncrementalCoordinator

	// result is the durable GenerateResult assembled across phases.
	result *GenerateResult

	// skeletons carries the per-language document skeletons rendered at
	// SceneTextReady by the parallel fan-out (early DocsPrepare). Nil in
	// serial mode / when the renderer does not implement the early/late split.
	skeletons map[Language]string

	// snapshot is the read-only scene-text projection taken before the
	// concurrent TTS and VidRush-prepare branches start.
	snapshot []sceneTextSnapshot
}

// measure runs one named business phase on the canonical Run clock, keeping
// the phases' bool contract. It is the single owner of phase timing + metrics.
func (e *executionRun) measure(stage kernobs.StageName, fn func(context.Context) bool) bool {
	return e.r.measurePhase(e.ctx, stage, fn)
}

// checkpoint persists the current durable partial result (best-effort). It is
// the single owner of checkpoint boundaries; phases never save partial state
// ad hoc.
func (e *executionRun) checkpoint() {
	e.r.checkpoint(e.ctx, e.runID, e.result)
}

// fail classifies a terminal error and stops the run. It is the single owner
// of error classification (error_code, failed_stage, attempt_count,
// next_retry_at) via failRunWithRetry, and always returns false so a phase
// can `return e.fail(...)`.
func (e *executionRun) fail(stage Stage, err error) bool {
	e.r.failRunWithRetry(e.ctx, e.runID, stage, err)
	return false
}

// log is the single logging seam for phases (nil-logger safe).
func (e *executionRun) log(msg string, fields ...zap.Field) {
	if e.r.log != nil {
		e.r.log.Info(msg, append([]zap.Field{zap.String("run_id", e.runID)}, fields...)...)
	}
}

// start resolves the canonical routing context, loads resume state from the
// repository, sets the run RUNNING for new runs, and derives the resume
// index + attempt. It returns false (already completed / terminal error)
// when the workflow must not continue.
func (e *executionRun) start() bool {
	e.log("scriptgeneration: starting execution",
		zap.String("source_type", string(e.req.Source.Type)),
	)

	// Resolve the canonical artifact routing context ONCE. A docs.enabled=true
	// run with no resolvable folder fails closed here, before any I/O.
	routing, resolveErr := e.req.resolveArtifactRoutingContext(e.r.scriptDocsFolderID)
	if resolveErr != nil {
		return e.fail(StagePublishingDocuments, resolveErr)
	}
	e.routing = routing

	// Determine resume stage from the existing run (if any).
	run, err := e.r.repo.Get(e.ctx, e.runID)
	e.resumeIdx = -1 // -1 means start from beginning
	if err == nil && run != nil {
		resumeStage := ResumeFrom(run)
		if resumeStage == StageCompleted {
			e.r.log.Info("run already completed", zap.String("run_id", e.runID))
			return false
		}
		e.resumeIdx = StageIndex(resumeStage)
		e.r.log.Info("resuming from checkpoint",
			zap.String("run_id", e.runID),
			zap.String("resume_stage", string(resumeStage)),
			zap.Int("attempt", run.AttemptCount+1),
		)
		e.run = run
		// Adopt the durable result from the checkpoint. Repository.Get returns
		// the checkpointed Result, and every phase that runs BEFORE the resume
		// index is skipped — so without this adoption a resume from
		// PUBLISHING_DOCUMENTS (including a resume from the CORE_READY boundary)
		// would continue against an empty result and publish nothing, silently
		// discarding the completed core work the checkpoint was written to
		// preserve. A fresh run has no checkpointed result, so this is a no-op
		// there.
		if e.result == nil && run.Result != nil {
			e.result = run.Result
		}
	} else {
		// New run — set RUNNING.
		if err := e.r.updateStage(e.ctx, e.runID, RunStatusRunning, StageNormalizing); err != nil {
			return e.fail(StageNormalizing, err)
		}
	}
	if e.exec.Attempt <= 0 {
		e.exec.Attempt = 1
		if e.run != nil && e.run.AttemptCount > 0 {
			e.exec.Attempt = e.run.AttemptCount + 1
		}
	}
	return true
}

// normalize runs the Stage 1 normalize phase.
func (e *executionRun) normalize() bool {
	return e.measure(kernobs.StageName(stageNormalize), func(c context.Context) bool {
		return e.r.runNormalizePhase(c, e.runID, e.exec, e.resumeIdx)
	})
}

// mediaPreflightPhase runs the P0.5 fail-fast media verification after
// normalization and before any scene-text generation. It is deliberately
// synchronous: fixed intro/outro assets, original audio, and source windows
// must be certified before the LLM, translator, or TTS can start.
func (e *executionRun) mediaPreflightPhase() bool {
	return e.measure(kernobs.StageName(StagePreflight), func(c context.Context) bool {
		if stageSkipped(e.resumeIdx, StagePreflight) {
			return true
		}
		if err := e.r.updateStage(c, e.runID, RunStatusRunning, StagePreflight); err != nil {
			return e.fail(StagePreflight, err)
		}
		if e.r.mediaPreflight == nil {
			if e.req.Intro == nil && e.req.Outro == nil {
				return true
			}
			result := PreflightResult{Failures: []PreflightFailure{{
				Category: "fixed_media",
				Detail:   "media preflight is not wired — fixed media cannot be certified before generation",
			}}}
			return e.fail(StagePreflight, result.AsError())
		}
		result := e.r.mediaPreflight.Run(c, e.req)
		if err := result.AsError(); err != nil {
			e.r.log.Warn("media preflight FAILED — run aborted before generation",
				zap.String("run_id", e.runID),
				zap.String("failures", result.Error()))
			return e.fail(StagePreflight, err)
		}
		e.r.log.Info("media preflight completed",
			zap.String("run_id", e.runID),
			zap.Int64("wall_ms", result.WallMS))
		return true
	})
}

// beginVidRushPhase registers run-scoped VidRush wiring only after the
// synchronous media preflight has passed. The coordinator lifetime remains
// owned by the caller so concurrent runs stay isolated.
func (e *executionRun) beginVidRushPhase() bool {
	var beginVidRushErr error
	kernobs.MeasureStage(e.ctx, "begin_vidrush", func(stageCtx context.Context) error {
		e.coordinator, beginVidRushErr = e.r.beginVidRush(stageCtx, e.runID, e.req)
		return beginVidRushErr
	})
	if beginVidRushErr != nil {
		return e.fail(StageNormalizing, beginVidRushErr)
	}
	return true
}

// generate runs scene-text generation (the Gemma phase) and records the
// generate-phase KPI milestones. It runs only after media preflight passes.
func (e *executionRun) generate() bool {
	ok := e.measure(kernobs.StageGenerate, func(c context.Context) bool {
		var phaseOK bool
		e.result, phaseOK = e.r.runSceneTextPhase(c, e.runID, e.req, e.routing, e.exec, e.run, e.resumeIdx)
		return phaseOK
	})
	if !ok {
		return false
	}

	// ── Pipeline KPI: generate phase milestones ────────────────
	// Streaming mode records first_scene_ready earlier via the coordinator;
	// serial/clip mode records it here at phase completion.
	if run := kernobs.FromContext(e.ctx); run != nil {
		elapsed := run.ElapsedMs()
		if run.Report().KPIs.GenerateFirstSceneReadyMs == 0 {
			kernobs.RecordKPIMilestone(e.ctx, "generate_first_scene_ready_ms", elapsed)
		}
		kernobs.RecordKPIMilestone(e.ctx, "generate_finished_ms", elapsed)
	}
	return true
}

// ensureResult guarantees a non-nil result for runs that produced no scenes.
func (e *executionRun) ensureResult() {
	if e.result == nil {
		e.result = &GenerateResult{Scenes: []Scene{}, Render: e.req.Render, Title: e.req.Title, OutputName: e.req.OutputName, VoiceoverGroup: e.req.ScriptParams.VoiceoverGroup}
	}
}

// translate runs the translation phase on the canonical SceneTextReady
// boundary (it depends only on the final scene text).
func (e *executionRun) translate() bool {
	return e.measure(kernobs.StageName(stageTranslation), func(c context.Context) bool {
		return e.r.runTranslationPhase(c, e.runID, e.req, e.exec, e.resumeIdx, e.result)
	})
}

// sceneTextReady runs the SceneTextReady fan-out in parallel with TTS. It owns
// the prepare-branch concurrency lifecycle (cancellation, join, projection)
// and the checkpoint after the fan-out. Returns false on a terminal error.
//
// Translated NLP is part of the fan-out, not a post-join step: it depends on
// the translations and the SOURCE annotations only, never on TTS, so the
// parallel path runs it on the semantic branch while TTS is still in flight
// (see parallelFanOut).
func (e *executionRun) sceneTextReady() bool {
	e.snapshot = snapshotSceneText(e.result.Scenes, e.req.SourceLanguage)

	ok := e.parallelFanOut()
	if ok {
		e.result.SourceTrace = sourceTraceFromResult(e.result)
	}
	return ok
}

// parallelFanOut runs the production SceneTextReady DAG: the VidRush join +
// overlay.prepare branch (DocsPrepare + audio prefetch) runs concurrently
// with TTS, then the branch is joined and its projections applied.
func (e *executionRun) parallelFanOut() bool {
	prepareCtx, cancelPrepare := context.WithCancel(e.ctx)
	defer cancelPrepare()
	// Start semantic enrichment first and independently. The old version put
	// DocsPrepare + audio prefetch in front of runVidRushJoinAndPrepare inside
	// one goroutine; a slow document skeleton therefore delayed NLP until after
	// TTS, even though the outer fan-out was nominally parallel.
	semanticDone := make(chan vidRushPrepareOutcome, 1)
	go func() {
		res, err := e.r.runVidRushJoinAndPrepare(prepareCtx, e.runID, e.req, e.snapshot)
		if err != nil {
			semanticDone <- vidRushPrepareOutcome{err: err}
			return
		}
		// Translated NLP rides the SAME branch as the prepare it depends on.
		// It needs the translations (final since the translate phase) and the
		// source annotations just computed — never TTS. Computing it here
		// overlaps it with the TTS branch still in flight instead of queueing it
		// behind the global join, where it used to be pure serial tail latency.
		//
		// It computes VALUES only: the application runs on this phase goroutine
		// after the join, because the TTS writer snapshots whole Scene structs
		// (localizedRenderClipFields) and marshals the result per unit, so any
		// concurrent write onto result.Scenes would be a data race.
		localized, nlpErr := e.r.computeLocalizedAnnotations(prepareCtx, e.req, e.result, res.annotations)
		semanticDone <- vidRushPrepareOutcome{result: res, localizedAnnotations: localized, nlpErr: nlpErr}
	}()

	// DocsPrepare and audio prefetch are independent of semantic enrichment;
	// keep them on a second branch so they also overlap both NLP and TTS.
	assetsDone := make(chan vidRushPrepareOutcome, 1)
	go func() {
		// Early DocsPrepare: render the scene-text-only document skeleton
		// first so CPU render overlaps both TTS and NLP.
		skel := e.r.renderDocumentSkeletons(e.req, e.result)

		// ── P1.1 Audio prefetch ──────────────────────────────
		// Resolve BGM/SFX assets and materialize original clip audio in
		// parallel with TTS. Best-effort: skip when the source is nil.
		var prefetched *AudioPrefetchResult
		if e.r.audioAssetSource != nil && (len(e.req.BackgroundMusic) > 0 || len(e.req.SoundEffects) > 0 ||
			e.req.MixPolicy.Normalize() == capabilityaudio.MixVoiceoverWithDuckedClip) {
			bgmIDs := make([]string, len(e.req.BackgroundMusic))
			for i, b := range e.req.BackgroundMusic {
				bgmIDs[i] = b.AssetID
			}
			sfxIDs := make([]string, len(e.req.SoundEffects))
			for i, s := range e.req.SoundEffects {
				sfxIDs[i] = s.AssetID
			}
			var clipIDs []string
			for _, s := range e.snapshot {
				clipIDs = append(clipIDs, s.ClipIDs...)
			}
			var clipAudioSource ClipAudioAssetSource
			if candidate, ok := e.r.audioAssetSource.(ClipAudioAssetSource); ok {
				clipAudioSource = candidate
			}
			pf, pfErr := PrefetchAudioAssets(prepareCtx, bgmIDs, sfxIDs, e.r.audioAssetSource, clipIDs, clipAudioSource, e.req.MixPolicy)
			if pfErr != nil {
				e.r.log.Warn("audio prefetch failed — audio compile will run with synchronous resolution",
					zap.String("run_id", e.runID),
					zap.Error(pfErr))
			} else {
				prefetched = pf
			}
		}
		assetsDone <- vidRushPrepareOutcome{
			skeletons:  skel,
			prefetched: prefetched,
		}
	}()

	// TTS runs in the main goroutine, in parallel with the prepare branch.
	if !e.measure(kernobs.StageName(voiceoverStage), func(c context.Context) bool {
		return e.r.runVoiceoverPhase(c, e.runID, e.req, e.routing, e.exec, e.resumeIdx, e.result)
	}) {
		// TTS failed: the deferred cancelPrepare stops the prepare branch.
		return false
	}

	// Join both early branches; an error fails the run (fail-closed). The
	// semantic branch is intentionally joined separately from the assets branch
	// so its start is never delayed by document rendering or audio prefetch.
	var semanticOutcome vidRushPrepareOutcome
	var assetsOutcome vidRushPrepareOutcome
	kernobs.MeasureStage(e.ctx, "prepare_join", func(stageCtx context.Context) error {
		semanticOutcome = <-semanticDone
		assetsOutcome = <-assetsDone
		return semanticOutcome.err
	})
	if semanticOutcome.err != nil {
		return e.fail(StageGeneratingSceneText, semanticOutcome.err)
	}
	applyVidRushPrepareProjections(e.result, semanticOutcome.result)
	if semanticOutcome.nlpErr != nil {
		return e.fail(StageTranslatingScenes, semanticOutcome.nlpErr)
	}
	// Apply the translated annotations on THIS goroutine: the semantic branch
	// only computed them, so the durable result has exactly one writer.
	applyLocalizedAnnotations(e.result, semanticOutcome.localizedAnnotations)
	e.skeletons = assetsOutcome.skeletons
	// Store the prefetched audio assets so the audio-compile phase consumes
	// them without blocking on I/O.
	e.result.AudioPrefetch = assetsOutcome.prefetched
	e.checkpoint()
	return true
}

// audioCompile runs the audio-compile + final-audio publish phases.
// audioCompile runs the four boundaries that used to share ONE stage label.
//
// They are separate stages because they are separate facts, owned by different
// systems: compiling audio, waiting for the video overlay render, projecting the
// editing timeline from the certified result, and uploading the artifact to
// Drive. Measuring them as one made `audio_compile` report a wall time dominated
// by a render it does not own, hid the render from the critical path, and
// reported drive.upload as the audio stage's dominant operation.
//
// The split is into SIBLINGS, not into a wrapper: the breakdown attributes a
// stage nested inside another stage to its enclosure, so a wrapper would change
// nothing. The order is the pre-split order — render before the editing-timeline
// projection (the projection carries the certified render artifact), and publish
// last.
func (e *executionRun) audioCompile() bool {
	var state audioCompileState
	if !e.measure(kernobs.StageName(audioCompileStage), func(c context.Context) bool {
		return e.r.runAudioCompilePhase(c, e.runID, e.req, e.exec, e.resumeIdx, e.result, &state)
	}) {
		return false
	}
	if !e.measure(StageOverlayRender, func(c context.Context) bool {
		return e.r.runOverlayRenderPhase(c, e.runID, e.req, e.exec, e.resumeIdx, state, e.result)
	}) {
		return false
	}
	if !e.measure(StageAudioFinalize, func(c context.Context) bool {
		return e.r.runAudioFinalizePhase(c, e.runID, e.exec, state, e.result)
	}) {
		return false
	}
	return e.measure(StageAudioPublish, func(c context.Context) bool {
		return e.r.publishFinalAudio(c, e.runID, e.req, e.routing, e.exec, e.result)
	})
}

// persist stores the canonical script only. The script-generation runtime
// produces localized clip artifacts; complete-video assembly is outside this
// capability and is not part of the script.generate contract.
func (e *executionRun) persist() bool {
	if !e.measure(kernobs.StageName(stagePersistence), func(c context.Context) bool {
		e.checkpoint()
		return e.r.persistScript(c, e.runID, e.req, e.exec, e.resumeIdx, e.result)
	}) {
		return false
	}
	// The core is durable from here: see markCoreReady.
	e.markCoreReady()
	return true
}

// markCoreReady records the CORE_READY boundary. At this point the certified
// overlay render, the published final audio and the canonical script row are
// durable, and only the post-processing legs remain: Google Docs publication
// inside this run, then the artifact/Drive finalization the worker performs
// after this run returns.
//
// The boundary is observability, never a terminal state. It exists because
// SUCCEEDED has to keep meaning "every requested artifact is published": the
// core becoming available earlier must be visible WITHOUT pretending that
// Docs and Drive are done. Consumers read `current_stage == CORE_READY` (and
// the core_ready_ms KPI) instead of inferring availability from the tail.
//
// It deliberately stays silent in two cases:
//   - no post-processing leg is deferred (docs disabled), where CORE_READY
//     would just be a second name for COMPLETED;
//   - the core contract does not hold, because the milestone must never
//     advertise a core that is not durable (NO-FAKE-AVAILABILITY).
func (e *executionRun) markCoreReady() {
	docsEnabled, docsLangs, _ := e.req.ResolveDocsConfig()
	if !docsEnabled || len(docsLangs) == 0 {
		return
	}
	if !IsCoreCompletable(e.result, docsLangs) {
		return
	}

	started := time.Now()
	kernobs.RecordStage(e.ctx, kernobs.StageInfo{Stage: kernobs.StageName(StageCoreReady)}, started, time.Now(), nil)
	if run := kernobs.FromContext(e.ctx); run != nil {
		kernobs.RecordKPIMilestone(e.ctx, "core_ready_ms", run.ElapsedMs())
	}
	// Durable projection: CurrentStage becomes CORE_READY, which is both what
	// a resumed attempt reads to re-enter at the post-processing legs
	// (ResumeFrom) and what an availability consumer polls.
	if err := e.r.updateStage(e.ctx, e.runID, RunStatusRunning, StageCoreReady); err != nil {
		// An observation must never fail a run whose core work succeeded.
		e.log("scriptgeneration: core ready stage update failed", zap.String("error", err.Error()))
		return
	}
	e.log("scriptgeneration: core ready",
		zap.Bool("core_ready", true),
		zap.String("current_stage", string(StageCoreReady)),
		zap.Int("pending_document_languages", len(docsLangs)),
	)
}

// documents runs the document (Docs) publishing phase with the pre-rendered
// per-language skeletons.
func (e *executionRun) documents() bool {
	return e.measure(kernobs.StageName(stageDocument), func(c context.Context) bool {
		return e.r.runDocumentPhase(c, e.runID, e.req, e.routing, e.exec, e.resumeIdx, e.result, e.skeletons)
	})
}

// complete finalizes a successful run (render-set certification, critical-path
// summary, pipeline invariants) and marks it COMPLETED.
func (e *executionRun) complete() {
	if e.r.overlayPublicationDrainer != nil {
		// Run-scoped join: the pool is process-wide, so the drainer resolves
		// this run's batch from the run context instead of joining whatever
		// another concurrent run happens to have in flight.
		if err := e.r.overlayPublicationDrainer.Wait(e.ctx); err != nil {
			e.r.failRunWithRetry(e.ctx, e.runID, StagePublishingDocuments,
				fmt.Errorf("overlay publication pool: %w", err))
			return
		}
		// Drive identity is written onto the certified artifact by the
		// publisher. Persist the final projection before marking the run
		// complete so a restart never loses a successful upload.
		e.r.checkpoint(e.ctx, e.runID, e.result)
	}
	e.r.completeRun(e.ctx, e.runID, e.result)
}

// processFixedDisplayText translates fixed-media display text into every
// target language. Fixed media never enters TTS/narration, but its display
// text remains a subtitle surface for localized renders.
func (c *sceneReadyCoordinator) processFixedDisplayText(out Scene) (Scene, error) {
	if !out.ExecutionMode.AllowsDisplayTextTranslation() {
		return out, nil
	}
	if out.Text == nil {
		out.Text = make(map[Language]string)
	}
	sourceText := strings.TrimSpace(out.Text[c.req.SourceLanguage])
	if sourceText == "" {
		return out, nil
	}
	langs := make([]Language, 0, len(c.req.Languages))
	seen := map[Language]bool{}
	for _, lang := range c.req.Languages {
		if lang == "" || lang == c.req.SourceLanguage || seen[lang] {
			continue
		}
		seen[lang] = true
		if out.Text[lang] != "" {
			continue
		}
		langs = append(langs, lang)
	}
	work := make([]sceneLanguageWork, 0, len(langs))
	for _, lang := range langs {
		work = append(work, sceneLanguageWork{lang: lang, needsTranslation: true})
	}
	outcomes, err := concurrent.Map(c.ctx, work, c.translationSlots.Cap(), func(ctx context.Context, itemIdx int, item sceneLanguageWork) (sceneLanguageOutcome, error) {
		translated, err := c.translateLanguage(ctx, itemIdx, out.ID, item.lang, sourceText)
		if err != nil {
			return sceneLanguageOutcome{}, err
		}
		return sceneLanguageOutcome{lang: item.lang, text: translated, translated: true}, nil
	})
	if err != nil {
		return Scene{}, err
	}
	for _, res := range outcomes {
		if res.translated {
			out.Text[res.lang] = res.text
		}
	}
	for _, res := range outcomes {
		if !res.translated {
			continue
		}
		if err := c.runner.recordArtifactOperation(c.ctx, c.exec, ArtifactOperation{
			OperationID: artifactOperationID(c.exec.Attempt, OperationTranslation, out.ID, string(res.lang)),
			Kind:        OperationTranslation,
			SceneID:     out.ID,
			Language:    res.lang,
			Status:      "COMPLETED",
		}); err != nil {
			return Scene{}, err
		}
	}
	c.mu.Lock()
	c.transCalls += len(outcomes)
	c.mu.Unlock()
	return out, nil
}

// sceneTextPathReason names why a run took the streaming or batch scene-text
// path. The runner owns the decision; this keeps the observability reason
// independent from the coordinator implementation.
func sceneTextPathReason(req GenerateRequest, streamed, topologyNeedsMaterialization bool, gen TextGenerator) string {
	switch {
	case streamed:
		return "streamed"
	case req.ScriptParams.SourceTextVerbatim:
		return "batch_source_text_verbatim"
	case len(req.MediaPlan.Extraction.ImportantPhrases) > 0:
		return "batch_important_phrase_hints"
	case req.Intro != nil || req.Outro != nil:
		return "batch_intro_outro"
	case topologyNeedsMaterialization:
		return "batch_segment_topology"
	case req.Source.Type == SourceClips && !SceneStreamingEligibility(req):
		return "batch_source_clips_ineligible"
	}
	if _, ok := gen.(SceneTextStreamer); !ok {
		return "batch_generator_not_streamable"
	}
	return "batch_reason_unclassified"
}
