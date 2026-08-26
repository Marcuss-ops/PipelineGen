// Package scriptgeneration — runner.go implements the durable
// stage-based execution of the script generation workflow. Each
// stage is executed in order, with checkpoint updates after every
// successful stage. A retry resumes from the last failed stage.
//
// Verdetto contract:
//
//	ScriptGenerationRunner
//	  ├─ Normalize
//	  ├─ GenerateSceneText
//	  ├─ TranslateScenes
//	  ├─ GenerateVoiceovers
//	  ├─ CompileAudio
//	  └─ UpsertDocuments
//
// Phase implementations live in runner_phase_*.go; this file retains the
// public Runner contract and linear orchestration.
// Resume-from-checkpoint: on retry, Execute reads the run from
// the repo and skips stages that are already checkpointed.
package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"
	capabilityimagesearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/imagesearch"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// enqueueOverlayPrepare builds the overlay.prepare job for the run's
// pre-timing OverlayIntents and submits it (fire-and-forget). The plan id is
// the run id — the same idempotency key the overlay plan uses — so a retry
// never double-prepares.
func (r *Runner) enqueueOverlayPrepare(ctx context.Context, runID string, req GenerateRequest, intents []capabilityoverlay.OverlayIntent) error {
	if r.overlayPrepareEnqueuer == nil || len(intents) == 0 {
		return nil
	}
	canvas := r.overlayCanvas.withDefaults()
	// overlay.prepare is a business stage boundary: the enqueue itself is
	// measured on the canonical Run clock so the run's critical path shows
	// the prepare submit wall time (which runs in parallel with TTS) instead
	// of hiding it inside the generate phase.
	if _, err := kernobs.MeasureStageReport(ctx, StageOverlayPrepare, func(stageCtx context.Context) error {
		return r.overlayPrepareEnqueuer.EnqueuePrepare(stageCtx, capabilityoverlay.PrepareRequest{
			SchemaVersion: capabilityoverlay.SchemaVersionPrepare,
			PlanID:        runID,
			VideoID:       runID,
			ProjectID:     strings.TrimSpace(req.Project),
			Width:         canvas.Width,
			Height:        canvas.Height,
			FPSNum:        canvas.FPSNum,
			FPSDen:        canvas.FPSDen,
			Intents:       intents,
		})
	}); err != nil {
		return err
	}
	return nil
}

// runVidRushJoinAndPrepare is the background branch of the SceneTextReady
// fan-out: it awaits the VidRush barrier, computes the per-scene entity
// annotations and the pre-timing OverlayIntents from a read-only scene-text
// snapshot, and enqueues overlay.prepare. It runs concurrently with TTS and
// never touches the mutable result (or result.Scenes), so prepare starts as
// soon as NLP results arrive without waiting for TTS or final audio and
// without racing the TTS writer. The caller applies the returned projections
// to result after the join.
func (r *Runner) runVidRushJoinAndPrepare(ctx context.Context, runID string, req GenerateRequest, snapshot []sceneTextSnapshot) (vidRushPrepareResult, error) {
	// Final VidRush barrier: wait only for enrichments still running, never
	// re-running the whole-document EntitiesProcessor. The fenced per-scene
	// results are projected onto the durable result after the join.
	segments, err := r.waitForVidRush(ctx, runID)
	if err != nil {
		return vidRushPrepareResult{}, err
	}
	annotations := computeSegmentEntityAnnotations(snapshot, req.SourceLanguage, segments)
	var intents []capabilityoverlay.OverlayIntent
	if r.overlayRegistry != nil {
		intents = planOverlayIntentsForAnnotations(snapshot, annotations, r.overlayRegistry)
		// Submit overlay.prepare (fire-and-forget): it resolves templates and
		// prefetches entity assets independently of the timing-frozen render
		// path. overlay.render never waits for it — render enqueues only
		// after the canonical timing is frozen in the audio-compile phase.
		// Fail-closed — an enqueue error fails the run, never a silent no-op.
		if err := r.enqueueOverlayPrepare(ctx, runID, req, intents); err != nil {
			return vidRushPrepareResult{}, err
		}
	}
	return vidRushPrepareResult{segments: segments, annotations: annotations, intents: intents}, nil
}

func sourceTraceFromResult(result *GenerateResult) scriptpkg.SourceTrace {
	trace := scriptpkg.SourceTrace{}
	if result == nil {
		return trace
	}
	trace = result.SourceTrace
	seen := make(map[string]struct{})
	for _, scene := range result.Scenes {
		refs := scene.Clips
		if len(refs) == 0 && scene.Clip != nil {
			refs = []*ClipReference{scene.Clip}
		}
		for _, ref := range refs {
			if ref == nil || ref.ID == "" {
				continue
			}
			if _, ok := seen[ref.ID]; ok {
				continue
			}
			seen[ref.ID] = struct{}{}
			trace.AcceptedClipIDs = append(trace.AcceptedClipIDs, ref.ID)
		}
	}
	return trace
}

// Runner executes the durable script generation stages.
// Each stage is checkpointed so a retry resumes from the last
// failed stage.
type Runner struct {
	repo                  RunRepository
	textGen               TextGenerator
	translator            Translator
	voiceoverGen          VoiceoverGenerator
	docPublisher          DocumentPublisher
	documentRenderer      DocumentRenderer
	combinedAudioRenderer CombinedAudioRenderer
	finalAudioPublisher   FinalAudioPublisher
	finalVideoAssembler   FinalVideoAssembler
	finalVideoPublisher   FinalVideoPublisher
	// audioAssetSource turns the run's BGM/SFX asset_ids into verified
	// local paths + certified durations before the audio plan is compiled.
	// Nil means the audio intent block is not resolvable — a run that
	// carries BGM/SFX intents fails closed in the audio-compile phase.
	audioAssetSource    AudioAssetSource
	scriptPersistence   ScriptPersistence
	recorder            ExecutionRecorder
	sceneCommitObserver SceneCommitObserver
	vidRushBarrier      VidRushBarrier
	vidRushTiming       VidRushTimingRecorder
	generationGate      *GenerationGate
	nlpGenerationGate   *GenerationGate
	vidRushPipeline     *VidRushPipeline

	// ttsConcurrency bounds the TTS voiceover worker pool: the voiceover
	// phase fans out scene×language synthesis to at most this many concurrent
	// calls. It defaults to DefaultTTSConcurrency; SetTTSConcurrency overrides
	// it. Docs publishing and Rust final-audio render stay single-threaded.
	ttsConcurrency int
	// translationConcurrency bounds concurrent scene×language translation calls.
	translationConcurrency int

	// serialMode reproduces the pre-parallel "before" chain for controlled
	// benchmarking: the VidRush/NLP join + overlay.prepare runs blocking
	// BEFORE TTS (entities → voiceover, never overlapping), and both the NLP
	// extraction and TTS pools are forced to concurrency 1. Default false
	// (the parallel SceneTextReady DAG).
	serialMode bool

	// vidRushRuns is the per-run VidRush wiring registry. beginVidRush
	// registers the fresh coordinator for its run so concurrent runs on this
	// shared Runner never observe each other's coordinator (the pre-registry
	// design stored the coordinator in the single observer/barrier/timing
	// fields, so run B overwrote run A's wiring and A's scene commits landed
	// in B's coordinator, failing closed with a run mismatch). The single
	// fields below remain as the injection seam for tests and as the fallback
	// when a run has no registered wiring.
	vidRushMu   sync.Mutex
	vidRushRuns map[string]vidRushWiring

	// scriptDocsFolderID is the configured default script documents
	// destination (PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID). It is resolved ONCE
	// against the caller's docs.folder_id by resolveArtifactRoutingContext
	// at run start; no document phase re-derives it. Empty means "not
	// configured" — a docs.enabled=true run fails closed.
	scriptDocsFolderID string

	// overlayCanvas is the target render canvas for the derived OverlayPlan.
	// Zero means the production contract default (1920×1080 @ 24 FPS) applies.
	overlayCanvas OverlayCanvasSpec

	// overlayRegistry is the canonical entity type→template registry used
	// by the EntityOverlayPlanner. Nil means overlay intent planning is
	// skipped (no overlay intents created).
	overlayRegistry *capabilityoverlay.ChrononOverlayRegistry

	// overlayPrepareEnqueuer submits the overlay.prepare job for the run's
	// pre-timing OverlayIntents. Nil means prepare is not registered (a
	// legitimate no-op for environments without a RenderingGen queue);
	// non-nil is fail-closed (an enqueue error fails the run).
	overlayPrepareEnqueuer OverlayPrepareEnqueuer
	overlayRenderEnqueuer  OverlayRenderEnqueuer
	// localizedRenderEnqueuer is the per-(scene, language) localized render
	// fan-out. Nil means render is not registered (no-op); non-nil is
	// fail-closed. It is the seam the SceneTextReady fan-out fires the moment
	// one scene's translation + TTS for a language are ready.
	localizedRenderEnqueuer LocalizedRenderEnqueuer

	// checkpoints is the optional durable per-unit checkpoint resolver. Nil
	// keeps the legacy best-effort idempotency (restored partial result
	// only); when wired, unit reuse (resume) is additionally gated by the
	// durable checkpoint — input fingerprint + artifact verification +
	// processor version — and completed units are recorded durably.
	checkpoints *capcheckpoint.Resolver

	// localizedRenderMu fences appends of certified localized-render videos
	// onto the run result. The fan-out enqueues from concurrent TTS workers
	// outside the per-phase apply lock, so the result slice needs its own
	// fence (a data race on LocalizedRenders would corrupt the run record).
	localizedRenderMu sync.Mutex

	// voiceoverPublishDrainer drains the async voiceover publish pool
	// (P0.4: separate TTS pool from publish pool). After the voiceover
	// phase completes, the runner calls Wait() to ensure all Drive
	// uploads + timing publishes + DB commits have finished before
	// audio compile and docs stages read the results. Nil means
	// synchronous publish (backward compat).
	voiceoverPublishDrainer interface{ Wait() }

	// mediaPreflight runs the fail-fast asset verification in parallel
	// with Gemma (P0.5). When wired, the preflight verifies clip files,
	// original audio streams, BGM/SFX assets, Drive folders, and watermark
	// assets AFTER normalize and concurrently with scene text generation.
	// Nil means the preflight is skipped (backward compat for tests).
	// The preflight result is joined after runSceneTextPhase; failures
	// fail the run before TTS is invoked.
	mediaPreflight MediaPreflight

	// recordSubStage records a sub-stage observation on the canonical
	// Run clock (P1.2 observability gap closure). It always measures
	// wall-clock time from the caller's perspective via time.Since, and
	// delegates to kernobs.RecordStage so the kernel never re-times it.
	// When no Run is bound to ctx, the call is a no-op.
	recordSubStage func(ctx context.Context, stage string, started time.Time, err error)

	// imageSearchResolver is the deterministic Image Search Intent resolver
	// (capabilities/imagesearch): the editorial/visual decision layer the
	// golden battery certifies (entity typing + canonicalization + query
	// order + no-image gate + negation + coreference). It consumes the SAME
	// entity extractor the VidRush pipeline uses, so production sees the
	// same deterministic path the battery certifies. Nil means the image
	// search decision surface is not wired (queries fall back to the legacy
	// ad-hoc builders).
	imageSearchResolver *capabilityimagesearch.Resolver

	log *zap.Logger
}

// vidRushWiring bundles the run-scoped VidRush coordinator behind all three
// runner seams (observer, barrier, timing recorder) so a single registry entry
// wires the same coordinator for the run.
type vidRushWiring struct {
	observer SceneCommitObserver
	barrier  VidRushBarrier
	timing   VidRushTimingRecorder
}

// NewRunner constructs the Runner with all required ports.
func NewRunner(
	repo RunRepository,
	textGen TextGenerator,
	translator Translator,
	voiceoverGen VoiceoverGenerator,
	docPublisher DocumentPublisher,
	documentRenderers ...DocumentRenderer,
) *Runner {
	if repo == nil {
		panic("scriptgeneration: RunRepository is required for Runner")
	}
	var documentRenderer DocumentRenderer
	if len(documentRenderers) > 0 {
		documentRenderer = documentRenderers[0]
	}
	return &Runner{
		repo:                   repo,
		textGen:                textGen,
		translator:             translator,
		voiceoverGen:           voiceoverGen,
		docPublisher:           docPublisher,
		documentRenderer:       documentRenderer,
		recorder:               noopExecutionRecorder{},
		vidRushRuns:            make(map[string]vidRushWiring),
		ttsConcurrency:         DefaultTTSConcurrency,
		translationConcurrency: DefaultTranslationConcurrency,
		log:                    zap.NewNop(),
	}
}

// SetDocumentRenderer wires the canonical presentation adapter after runner
// construction. This keeps the capability testable without importing an HTML
// or Drive implementation into the capability package.
func (r *Runner) beginVidRush(ctx context.Context, runID string, req GenerateRequest) (*VidRushIncrementalCoordinator, error) {
	// Request-level kill switch: a caller that explicitly disables entity
	// extraction (output.extract_entities=disabled) skips the incremental
	// VidRush pipeline entirely — no per-scene entity extraction and no
	// provider fan-out derived from it. ToggleDefault/ToggleEnabled keep the
	// canonical always-extract behavior.
	if req.EntityExtractionDisabled() {
		return nil, nil
	}
	p := r.vidRushPipeline
	if p == nil || p.Enricher == nil {
		return nil, nil
	}
	if p.PlanResolver == nil {
		return nil, fmt.Errorf("scriptgeneration: vidrush pipeline requires a plan resolver")
	}
	plan, err := p.PlanResolver.ResolveVidRushPlan(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("resolve vidrush plan: %w", err)
	}
	backpressure := p.Backpressure
	if r.serialMode {
		backpressure.ExtractionLimit = 1
	}
	coordinator := NewVidRushIncrementalCoordinatorWithBackpressure(p.Enricher, plan, backpressure)
	coordinator.SetSegmentProviderResolver(p.ProviderResolver)
	coordinator.SetSegmentMaterializer(p.Materializer)
	coordinator.SetMetrics(p.Metrics)
	nlpGate := r.nlpGenerationGate
	// Compatibility fallback for focused tests and older composition roots
	// that only wired the historical shared gate. Production wiring always
	// provides both independent gates.
	if nlpGate == nil {
		nlpGate = r.generationGate
	}
	coordinator.SetGenerationGate(nlpGate)

	r.registerVidRush(runID, vidRushWiring{
		observer: coordinator,
		barrier:  coordinator,
		timing:   coordinator,
	})
	return coordinator, nil
}

// endVidRush unregisters only this run's VidRush wiring so the run's
// coordinator is released without touching any other concurrent run's wiring.
func (r *Runner) endVidRush(runID string) {
	r.unregisterVidRush(runID)
}

// registerVidRush records the per-run wiring under its run ID.
func (r *Runner) registerVidRush(runID string, w vidRushWiring) {
	r.vidRushMu.Lock()
	defer r.vidRushMu.Unlock()
	if r.vidRushRuns == nil {
		r.vidRushRuns = make(map[string]vidRushWiring)
	}
	r.vidRushRuns[runID] = w
}

// unregisterVidRush removes the per-run wiring for runID.
func (r *Runner) unregisterVidRush(runID string) {
	r.vidRushMu.Lock()
	defer r.vidRushMu.Unlock()
	delete(r.vidRushRuns, runID)
}

// sceneCommitObserverFor resolves the observer for runID: the per-run wiring
// wins when registered; otherwise the single injected seam (tests) is used.
func (r *Runner) sceneCommitObserverFor(runID string) SceneCommitObserver {
	r.vidRushMu.Lock()
	defer r.vidRushMu.Unlock()
	if w, ok := r.vidRushRuns[runID]; ok {
		return w.observer
	}
	return r.sceneCommitObserver
}

// vidRushBarrierFor resolves the barrier for runID: the per-run wiring wins
// when registered; otherwise the single injected seam (tests) is used.
func (r *Runner) vidRushBarrierFor(runID string) VidRushBarrier {
	r.vidRushMu.Lock()
	defer r.vidRushMu.Unlock()
	if w, ok := r.vidRushRuns[runID]; ok {
		return w.barrier
	}
	return r.vidRushBarrier
}

// vidRushTimingFor resolves the timing recorder for runID: the per-run wiring
// wins when registered; otherwise the single injected seam (tests) is used.
func (r *Runner) vidRushTimingFor(runID string) VidRushTimingRecorder {
	r.vidRushMu.Lock()
	defer r.vidRushMu.Unlock()
	if w, ok := r.vidRushRuns[runID]; ok {
		return w.timing
	}
	return r.vidRushTiming
}

// markGenerationStart reports the start of scene-text generation to the run's
// timing recorder, when one is wired.
func (r *Runner) markGenerationStart(runID string, t time.Time) {
	if rec := r.vidRushTimingFor(runID); rec != nil {
		rec.MarkGenerationStart(t)
	}
}

// markGenerationComplete reports that generation finished emitting stable
// scenes to the run's timing recorder, when one is wired.
func (r *Runner) markGenerationComplete(runID string, t time.Time) {
	if rec := r.vidRushTimingFor(runID); rec != nil {
		rec.MarkGenerationComplete(t)
	}
}

// waitForVidRush awaits the final incremental-VidRush barrier for this run
// when one is wired. A nil barrier is a safe no-op (batch workflows may
// enrich the whole document later); when present, a barrier error fails the
// run fail-closed and no results are returned. On success it returns the
// fenced, canonically ordered per-scene enrichment results so the caller can
// project the durable entity aggregate onto the result surface.
func (r *Runner) waitForVidRush(ctx context.Context, runID string) ([]scriptpkg.VidRushSegmentResult, error) {
	barrier := r.vidRushBarrierFor(runID)
	if barrier == nil {
		return nil, nil
	}
	return barrier.WaitForVidRush(ctx, runID)
}

// Execute runs the complete generation workflow for the given run.
// It reads the run from the repository and resumes from the last
// checkpointed stage, skipping already-completed stages.
//
// Resume flow:
//  1. Read GenerationRun from repo (or use provided req for new runs)
//  2. Determine resume stage via ResumeFrom()
//  3. Skip stages before the resume stage
//  4. Execute from resume stage onward with checkpoint after each
//  5. On failure: persist error, return
//  6. On completion: persist final result, mark COMPLETED
//
// The handler must NOT wait for Execute to complete. This method
// is intended to be launched as a goroutine.
func (r *Runner) Execute(ctx context.Context, runID string, req GenerateRequest) {
	r.ExecuteWithContext(ctx, runID, req, NewExecutionContext(runID, req.IdempotencyKey))
}

// ExecuteWithContext is the worker-facing entry point that preserves the
// broker's root/parent/project/video correlation across every stage.
func (r *Runner) ExecuteWithContext(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext) {

	if exec.JobID == "" {
		exec.JobID = runID
	}
	if exec.RootJobID == "" {
		exec.RootJobID = exec.JobID
	}
	if exec.CorrelationID == "" {
		exec.CorrelationID = req.IdempotencyKey
	}
	if err := exec.Validate(); err != nil {
		if r.log != nil {
			r.log.Warn("scriptgeneration: invalid execution context", zap.Error(err))
		}
		r.failRunWithRetry(ctx, runID, StageNormalizing, err)
		return
	}
	r.log.Info("scriptgeneration: starting execution",
		zap.String("run_id", runID),
		zap.String("source_type", string(req.Source.Type)),
	)

	// Resolve the canonical artifact routing context ONCE at generation
	// start. Downstream phases consume this resolved value and never re-derive
	// Project, Language, or folder routing (godlike/06 SSOT — one owner per
	// routing fact). A docs.enabled=true run with no resolvable folder fails
	// closed here, before any LLM or Google Docs I/O.
	routing, resolveErr := req.resolveArtifactRoutingContext(r.scriptDocsFolderID)
	if resolveErr != nil {
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, resolveErr)
		return
	}

	// Determine resume stage from existing run (if any).
	run, err := r.repo.Get(ctx, runID)
	resumeIdx := -1 // -1 means start from beginning
	if err == nil && run != nil {
		resumeStage := ResumeFrom(run)
		if resumeStage == StageCompleted {
			r.log.Info("run already completed", zap.String("run_id", runID))
			return
		}
		resumeIdx = StageIndex(resumeStage)
		r.log.Info("resuming from checkpoint",
			zap.String("run_id", runID),
			zap.String("resume_stage", string(resumeStage)),
			zap.Int("attempt", run.AttemptCount+1),
		)
	} else {
		// New run — set RUNNING.
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageNormalizing); err != nil {
			r.failRunWithRetry(ctx, runID, StageNormalizing, err)
			return
		}
	}
	if exec.Attempt <= 0 {
		exec.Attempt = 1
		if run != nil && run.AttemptCount > 0 {
			exec.Attempt = run.AttemptCount + 1
		}
	}

	if !r.measurePhase(ctx, kernobs.StageName(stageNormalize), func(c context.Context) bool {
		return r.runNormalizePhase(c, runID, exec, resumeIdx)
	}) {
		return
	}
	var coordinator *VidRushIncrementalCoordinator
	var beginVidRushErr error
	kernobs.MeasureStage(ctx, "begin_vidrush", func(stageCtx context.Context) error {
		coordinator, beginVidRushErr = r.beginVidRush(stageCtx, runID, req)
		return beginVidRushErr
	})
	if beginVidRushErr != nil {
		r.failRunWithRetry(ctx, runID, StageNormalizing, beginVidRushErr)
		return
	}
	if coordinator != nil {
		defer r.endVidRush(runID)
	}

	// ── P0.5 Media Preflight (parallel with Gemma) ────────────────
	// After normalize, start the fail-fast asset verification in a
	// goroutine. It checks clip files, original audio streams, BGM/SFX,
	// and watermark assets while Gemma generates scene text. Join
	// after runSceneTextPhase; preflight failures fail the run BEFORE
	// any TTS work is done.
	var preflightDone <-chan PreflightResult
	if r.mediaPreflight != nil {
		pfCh := make(chan PreflightResult, 1)
		preflightDone = pfCh
		go func() {
			pfCh <- r.mediaPreflight.Run(ctx, req)
		}()
	}

	var result *GenerateResult
	if !r.measurePhase(ctx, kernobs.StageGenerate, func(c context.Context) bool {
		var ok bool
		result, ok = r.runSceneTextPhase(c, runID, req, routing, exec, run, resumeIdx)
		return ok
	}) {
		return
	}

	// ── Pipeline KPI: generate phase milestones ────────────────
	// For streaming mode, the coordinator records first_scene_ready
	// earlier; for serial/clip mode, we record it at phase completion.
	if run := kernobs.FromContext(ctx); run != nil {
		elapsed := run.ElapsedMs()
		// Only set first_scene_ready if not already set by coordinator.
		if run.Report().KPIs.GenerateFirstSceneReadyMs == 0 {
			kernobs.RecordKPIMilestone(ctx, "generate_first_scene_ready_ms", elapsed)
		}
		kernobs.RecordKPIMilestone(ctx, "generate_finished_ms", elapsed)
	}

	// ── Join media preflight ────────────────────────────────────
	if preflightDone != nil {
		preflightJoinStarted := time.Now()
		pfResult := <-preflightDone
		if pfResult.HasFailures() {
			kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: "media_preflight_join"}, preflightJoinStarted, time.Now(), fmt.Errorf("preflight: %d failures", len(pfResult.Failures)))
		} else {
			kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: "media_preflight_join"}, preflightJoinStarted, time.Now(), nil)
		}
		r.log.Info("media preflight completed",
			zap.String("run_id", runID),
			zap.Int("failures", len(pfResult.Failures)),
			zap.Int64("wall_ms", pfResult.WallMS))
		if pfResult.HasFailures() {
			r.log.Warn("media preflight FAILED — run aborted before TTS",
				zap.String("run_id", runID),
				zap.String("failures", pfResult.Error()))
			r.failRunWithRetry(ctx, runID, StagePreflight, fmt.Errorf("media preflight: %s", pfResult.Error()))
			return
		}
	}

	if result == nil {
		result = &GenerateResult{Scenes: []Scene{}, Render: req.Render, Title: req.Title, OutputName: req.OutputName, VoiceoverGroup: req.ScriptParams.VoiceoverGroup}
	}
	// ── SCENE-TEXT-READY FAN-OUT ──────────────────────────────────────
	// Committing the scene text during the generate phase is the canonical
	// SceneTextReady boundary. SceneAnalysis (VidRush) already started per
	// scene on that boundary; translation is the next branch (depends only on
	// the final scene text, never on entities/phrases/words).
	if !r.measurePhase(ctx, kernobs.StageName(stageTranslation), func(c context.Context) bool {
		return r.runTranslationPhase(c, runID, req, exec, resumeIdx, result)
	}) {
		return
	}

	snapshot := snapshotSceneText(result.Scenes, req.SourceLanguage)

	// skeletons carries the per-language document skeletons rendered at
	// SceneTextReady by the parallel fan-out (the early DocsPrepare pass). It
	// is nil in serial mode (the "before" baseline keeps the one-shot render)
	// and when the renderer does not implement the early/late split.
	var skeletons map[Language]string

	if r.serialMode {
		// Serial "before" chain: entities → voiceover. The VidRush join +
		// overlay.prepare runs blocking first, its projections are applied,
		// and only then does TTS start — NLP and TTS never overlap.
		prepared, err := r.runVidRushJoinAndPrepare(ctx, runID, req, snapshot)
		if err != nil {
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, err)
			return
		}
		applyVidRushPrepareProjections(result, prepared)

		if !r.measurePhase(ctx, kernobs.StageName(voiceoverStage), func(c context.Context) bool {
			return r.runVoiceoverPhase(c, runID, req, routing, exec, resumeIdx, result)
		}) {
			return
		}
		r.checkpoint(ctx, runID, result)
	} else {
		// ── SCENE-TEXT-READY FAN-OUT (parallel DAG) ────────────────────
		// The VidRush join + overlay.prepare is the OTHER branch of the
		// SceneTextReady fan-out, running concurrently with TTS below. It
		// awaits the VidRush barrier, computes the per-scene entity
		// annotations and the pre-timing OverlayIntents from a read-only
		// scene-text snapshot, and enqueues overlay.prepare — so prepare
		// starts as soon as NLP results arrive and never waits for TTS or
		// final audio. The branch never touches result (or result.Scenes), so
		// it runs alongside TTS without racing; the projections are applied
		// after the join below.
		prepareCtx, cancelPrepare := context.WithCancel(ctx)
		defer cancelPrepare()
		prepareDone := make(chan vidRushPrepareOutcome, 1)
		go func() {
			// Early DocsPrepare: render the scene-text-only document skeleton
			// for each docs language FIRST, before the VidRush join, so the
			// CPU render overlaps both TTS (main goroutine) and NLP (VidRush
			// enrichments) instead of waiting for the audio join.
			skel := r.renderDocumentSkeletons(req, result)

			// ── P1.1 Audio prefetch ──────────────────────────────
			// Resolve BGM/SFX assets and materialize original clip
			// audio in parallel with TTS so the heavy I/O is done
			// before audio compile starts. Best-effort: skip when
			// the required source is nil (audio compile handles the
			// fail-closed check at its own boundary).
			var prefetched *AudioPrefetchResult
			if (len(req.BackgroundMusic) > 0 || len(req.SoundEffects) > 0 ||
				req.MixPolicy.Normalize() == capabilityaudio.MixVoiceoverWithDuckedClip) &&
				r.audioAssetSource != nil {
				bgmIDs := make([]string, len(req.BackgroundMusic))
				for i, b := range req.BackgroundMusic {
					bgmIDs[i] = b.AssetID
				}
				sfxIDs := make([]string, len(req.SoundEffects))
				for i, s := range req.SoundEffects {
					sfxIDs[i] = s.AssetID
				}
				var clipIDs []string
				for _, s := range result.Scenes {
					for _, c := range s.Clips {
						if c != nil && c.ID != "" {
							clipIDs = append(clipIDs, c.ID)
						}
					}
					if s.Clip != nil && s.Clip.ID != "" {
						clipIDs = append(clipIDs, s.Clip.ID)
					}
				}
				var clipAudioSource ClipAudioAssetSource
				if candidate, ok := r.audioAssetSource.(ClipAudioAssetSource); ok {
					clipAudioSource = candidate
				}
				pf, pfErr := PrefetchAudioAssets(prepareCtx, bgmIDs, sfxIDs, r.audioAssetSource, clipIDs, clipAudioSource, req.MixPolicy)
				if pfErr != nil {
					r.log.Warn("audio prefetch failed — audio compile will run with synchronous resolution",
						zap.String("run_id", runID),
						zap.Error(pfErr))
				} else {
					prefetched = pf
				}
			}

			res, err := r.runVidRushJoinAndPrepare(prepareCtx, runID, req, snapshot)
			prepareDone <- vidRushPrepareOutcome{
				result:     res,
				skeletons:  skel,
				prefetched: prefetched,
				err:        err,
			}
		}()

		// TTS runs in the main goroutine, in parallel with the prepare branch.
		if !r.measurePhase(ctx, kernobs.StageName(voiceoverStage), func(c context.Context) bool {
			return r.runVoiceoverPhase(c, runID, req, routing, exec, resumeIdx, result)
		}) {
			// TTS failed: the deferred cancelPrepare stops the prepare branch.
			return
		}

		// Join the prepare branch; an error fails the run (fail-closed).
		// MeasureStage attributes this wait (VidRush + DocsPrepare + AudioPrefetch
		// finishing after TTS) so it no longer leaks into unattributed time.
		var outcome vidRushPrepareOutcome
		kernobs.MeasureStage(ctx, "prepare_join", func(stageCtx context.Context) error {
			outcome = <-prepareDone
			return outcome.err
		})
		if outcome.err != nil {
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, outcome.err)
			return
		}
		applyVidRushPrepareProjections(result, outcome.result)
		skeletons = outcome.skeletons
		// Store the prefetched audio assets so the audio compile phase
		// can consume them without blocking on I/O.
		result.AudioPrefetch = outcome.prefetched
		r.checkpoint(ctx, runID, result)
	}

	result.SourceTrace = sourceTraceFromResult(result)
	if !r.measurePhase(ctx, kernobs.StageName(audioCompileStage), func(c context.Context) bool {
		if !r.runAudioCompilePhase(c, runID, req, exec, resumeIdx, result) {
			return false
		}
		return r.publishFinalAudio(c, runID, req, routing, exec, result)
	}) {
		return
	}
	if !r.measurePhase(ctx, kernobs.StageName(stagePersistence), func(c context.Context) bool {
		if result.FinalVideoRequired {
			if err := r.assembleFinalVideo(c, runID, result); err != nil {
				r.failRunWithRetry(c, runID, StagePublishingDocuments, err)
				return false
			}
		}
		if result.FinalVideoRequired && r.finalVideoPublisher != nil {
			published, err := r.finalVideoPublisher.PublishFinalVideo(c, runID, *result.FinalVideo, req.Render.DriveFolderID)
			if err != nil {
				r.failRunWithRetry(c, runID, StagePublishingDocuments, fmt.Errorf("FINAL_VIDEO_UPLOAD_FAILED: %w", err))
				return false
			}
			result.FinalVideo.AssetID = published.AssetID
			result.FinalVideo.DriveLink = published.DriveLink
		} else if result.FinalVideoRequired {
			r.failRunWithRetry(c, runID, StagePublishingDocuments, fmt.Errorf("FINAL_VIDEO_UPLOAD_FAILED: final video publisher is not wired"))
			return false
		}
		r.checkpoint(c, runID, result)
		return r.persistScript(c, runID, req, exec, resumeIdx, result)
	}) {
		return
	}
	if !r.measurePhase(ctx, kernobs.StageName(stageDocument), func(c context.Context) bool {
		return r.runDocumentPhase(c, runID, req, routing, exec, resumeIdx, result, skeletons)
	}) {
		return
	}
	r.completeRun(ctx, runID, result)
}
