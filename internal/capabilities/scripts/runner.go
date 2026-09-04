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
	// re-running whole-document extraction. The fenced per-scene
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
	// recorderMu serializes calls into the execution recorder. Production
	// recorders are expected to be durable, but the port is not required to
	// implement internal concurrency; serialization also keeps the lineage
	// contract deterministic during parallel Docs/render fan-out.
	recorderMu sync.Mutex

	// voiceoverPublishDrainer drains the async voiceover publish pool
	// (P0.4: separate TTS pool from publish pool). After the voiceover
	// phase completes, the runner calls Wait() to ensure all Drive
	// uploads + timing publishes + DB commits have finished before
	// audio compile and docs stages read the results. Nil means
	// synchronous publish (backward compat).
	voiceoverPublishDrainer interface{ Wait() }

	// mediaPreflight runs the fail-fast asset verification after normalize
	// and before scene-text generation (P0.5). When wired, it verifies clip
	// assets, fixed-media original audio/source windows, BGM/SFX assets,
	// Drive folders, and watermark assets. Fixed-media requests fail closed
	// when the preflight is not wired; non-fixed legacy tests may omit it.
	mediaPreflight MediaPreflight
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
	if p == nil {
		return nil, nil
	}
	// Fase 1-5 semantic cutover (big-bang): when the new ports are wired,
	// build the SceneIRSegmentEnricher + SemanticProviderResolver and use
	// them in place of the legacy Enricher/ProviderResolver. The new chain
	// compiles a SceneIR (immutable identity), extracts source-grounded
	// entities via VisualNER, resolves candidates LOCAL FIRST via
	// stockintelligence, ranks via MediaSampler, and certifies via MediaCert.
	enricher := p.Enricher
	if p.NERPort != nil {
		newEnricher, err := NewSceneIRSegmentEnricher(p.NERPort)
		if err != nil {
			return nil, fmt.Errorf("vidrush pipeline: %w", err)
		}
		enricher = newEnricher
	}
	if enricher == nil {
		return nil, nil
	}
	providerResolver := p.ProviderResolver
	if p.StockResolverPort != nil && p.SamplerPort != nil {
		semanticResolver, err := NewSemanticProviderResolver(p.StockResolverPort, p.SamplerPort)
		if err != nil {
			return nil, fmt.Errorf("vidrush pipeline: %w", err)
		}
		if p.ProviderResolver != nil {
			providerResolver, err = NewSemanticAndFanoutResolver(semanticResolver, p.ProviderResolver)
			if err != nil {
				return nil, fmt.Errorf("vidrush pipeline: %w", err)
			}
		} else {
			providerResolver = semanticResolver
		}
	}
	// In an images-only plan the shared fanout still owns image discovery, but
	// the semantic stock resolver must not run the video/Artlist path.
	if req.MediaPlan.ProviderPolicy.Artlist == "disabled" {
		providerResolver = p.ProviderResolver
	}
	if p.PlanResolver == nil {
		return nil, fmt.Errorf("scriptgeneration: vidrush pipeline requires a plan resolver")
	}
	plan, err := p.PlanResolver.ResolveVidRushPlan(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("resolve vidrush plan: %w", err)
	}
	// The ingress media policy is an explicit caller contract. Keep it
	// authoritative at the coordinator boundary as well: plan builders may
	// enrich editorial fields, but they must not silently re-enable a provider
	// that the durable request disabled (notably Artlist in images-only runs).
	if plan != nil {
		requestedPolicy := req.MediaPlan.ProviderPolicy
		if requestedPolicy.Artlist != "" || requestedPolicy.YouTube != "" ||
			requestedPolicy.InternetImages != "" || requestedPolicy.ImageGeneration != "" {
			plan.MediaPlan.ProviderPolicy = requestedPolicy
		}
	}
	certSpec := p.CertSpec
	if p.CertSpecResolver != nil {
		certSpec = p.CertSpecResolver.ResolveMediaCertSpec(plan)
	}
	backpressure := p.Backpressure
	if r.serialMode {
		backpressure.ExtractionLimit = 1
	}
	coordinator := NewVidRushIncrementalCoordinatorWithBackpressure(enricher, plan, backpressure)
	coordinator.SetSegmentProviderResolver(providerResolver)
	coordinator.SetSegmentMaterializer(p.Materializer)
	coordinator.SetMetrics(p.Metrics)
	// The deterministic Image Search Intent resolver rides the run: its
	// canonical_entity_id decisions are stamped into every segment so the
	// annotation/media projection joins on the identity it chose.
	coordinator.SetImageSearchResolver(r.imageSearchResolver)
	nlpGate := r.nlpGenerationGate
	// Compatibility fallback for focused tests and older composition roots
	// that only wired the historical shared gate. Production wiring always
	// provides both independent gates.
	if nlpGate == nil {
		nlpGate = r.generationGate
	}
	coordinator.SetGenerationGate(nlpGate)

	// Fase 2 barrier wrap: when the MediaCertifierPort is wired, wrap the
	// coordinator's barrier so a CERTIFIED=false run fails the job even
	// when the inner barrier returned no error.
	barrier := VidRushBarrier(coordinator)
	if p.CertifierPort != nil {
		certBarrier, err := NewMediaCertBarrier(barrier, p.CertifierPort, certSpec)
		if err != nil {
			return nil, fmt.Errorf("vidrush pipeline: %w", err)
		}
		barrier = certBarrier
	}

	r.registerVidRush(runID, vidRushWiring{
		observer: coordinator,
		barrier:  barrier,
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
	if !r.prepareExecution(ctx, runID, req, &exec) {
		return
	}

	r.runExecution(ctx, runID, req, exec)
}

// prepareExecution normalizes and validates the correlation context before any
// workflow I/O. Keeping this boundary separate makes the public entry point
// intentionally small without changing failure handling.
func (r *Runner) prepareExecution(ctx context.Context, runID string, req GenerateRequest, exec *ExecutionContext) bool {
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
		return false
	}
	return true
}

// runExecution performs the ordered workflow after execution context
// validation. It drives the run through its explicit business phases via a
// run-scoped execution wrapper; the wrapper owns the shared timing, metrics,
// checkpoint, error-classification and logging concerns a single time. Each
// phase is an explicit named step below; a phase returning false stops the
// run (a terminal error was already classified and persisted).
func (r *Runner) runExecution(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext) {
	r.runExecutionPhases(ctx, runID, req, exec)
}
