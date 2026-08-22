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
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
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
			FPS:           canvas.FPS,
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
	// Zero means the golden canary default (1280×720 @ 30 FPS) applies.
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
func (r *Runner) SetDocumentRenderer(renderer DocumentRenderer) {
	if r != nil {
		r.documentRenderer = renderer
	}
}

// SetLogger sets the runner's logger. Nil-safe (no-op on nil).
func (r *Runner) SetLogger(log *zap.Logger) {
	if log != nil {
		r.log = log
	}
}

// SetSerialMode toggles the serial (pre-parallel "before") pipeline for
// controlled benchmarking. When enabled the NLP/entity branch completes
// blocking before TTS starts, and the NLP extraction + TTS pools are forced
// to concurrency 1. Disabling restores the parallel SceneTextReady DAG.
func (r *Runner) SetSerialMode(on bool) {
	if r == nil {
		return
	}
	r.serialMode = on
	if on {
		r.ttsConcurrency = 1
		r.translationConcurrency = 1
	} else {
		r.ttsConcurrency = DefaultTTSConcurrency
		r.translationConcurrency = DefaultTranslationConcurrency
	}
}

// SetScriptDocsFolderID wires the configured default script documents
// destination (PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID). Nil-safe. When empty,
// a docs.enabled=true generation fails closed at run start.
func (r *Runner) SetScriptDocsFolderID(folderID string) {
	if r != nil {
		r.scriptDocsFolderID = folderID
	}
}

// SetOverlayCanvas wires the target render canvas for the derived
// OverlayPlan. Nil-safe; a zero spec falls back to the golden canary
// default (1280×720 @ 30 FPS).
func (r *Runner) SetOverlayCanvas(canvas OverlayCanvasSpec) {
	if r != nil {
		r.overlayCanvas = canvas
	}
}

// SetOverlayRegistry wires the canonical entity type→template registry used
// by the EntityOverlayPlanner. Nil-safe; nil means overlay intent planning
// is skipped.
func (r *Runner) SetOverlayRegistry(registry *capabilityoverlay.ChrononOverlayRegistry) {
	if r != nil {
		r.overlayRegistry = registry
	}
}

// SetOverlayPrepareEnqueuer wires the overlay.prepare job enqueuer. Nil-safe;
// nil means prepare is not registered (no prepare job is enqueued).
func (r *Runner) SetOverlayPrepareEnqueuer(enqueuer OverlayPrepareEnqueuer) {
	if r != nil {
		r.overlayPrepareEnqueuer = enqueuer
	}
}

// SetOverlayRenderEnqueuer wires the timing-frozen overlay.render path.
func (r *Runner) SetOverlayRenderEnqueuer(enqueuer OverlayRenderEnqueuer) {
	if r != nil {
		r.overlayRenderEnqueuer = enqueuer
	}
}

// SetLocalizedRenderEnqueuer wires the per-(scene, language) localized render
// fan-out. A nil enqueuer disables the fan-out (render not registered); a
// non-nil enqueuer is fail-closed (an enqueue error fails the run).
func (r *Runner) SetLocalizedRenderEnqueuer(enqueuer LocalizedRenderEnqueuer) {
	if r != nil {
		r.localizedRenderEnqueuer = enqueuer
	}
}

// enqueueLocalizedRender emits one localized render for a ready (scene,
// language) unit. A voiceover is required only for the voiceover-driven path;
// explicit clip renders are also valid with audio.mode=NONE. Callers build
// the input with the values they already hold (under the per-unit lock when
// the unit's maps are shared across workers) so this helper performs no map
// reads of its own.
func (r *Runner) enqueueLocalizedRender(ctx context.Context, input LocalizedRenderInput) error {
	if r == nil {
		return nil
	}
	if input.Render.Enabled && r.localizedRenderEnqueuer == nil {
		return fmt.Errorf("localized render requested for scene %q but render enqueuer is not wired", input.SceneID)
	}
	if r.localizedRenderEnqueuer == nil {
		return nil
	}
	if input.Voiceover.ID == "" && strings.TrimSpace(input.ClipAssetID) == "" && strings.TrimSpace(input.ClipID) == "" {
		return nil
	}
	return r.localizedRenderEnqueuer.EnqueueLocalizedRender(ctx, input)
}

// recordLocalizedRender records one certified produced video of the
// localized render fan-out onto the run result. It is the durable proof
// that "this run produced this final MP4" (asset id, sha256, Drive link)
// — the produced video is never orphaned from the run that produced it.
// Fail-closed: the recorder and the result append must both succeed, and
// concurrent fan-out workers are fenced by localizedRenderMu.
func (r *Runner) recordLocalizedRender(ctx context.Context, exec ExecutionContext, result *GenerateResult, rendered LocalizedRenderResult) error {
	if strings.TrimSpace(rendered.AssetID) == "" {
		return nil
	}
	if result != nil {
		r.localizedRenderMu.Lock()
		result.LocalizedRenders = append(result.LocalizedRenders, rendered)
		r.localizedRenderMu.Unlock()
	}
	// Durable lineage: the produced video is an OperationRender artifact
	// joinable on (scene_id, language, asset_id) like every other produced
	// artifact of the run.
	return r.recordArtifactOperation(ctx, exec, ArtifactOperation{
		OperationID: artifactOperationID(exec.Attempt, OperationRender, rendered.SceneID, string(rendered.Language)),
		Kind:        OperationRender,
		SceneID:     rendered.SceneID,
		Language:    rendered.Language,
		AssetID:     rendered.AssetID,
		Status:      "COMPLETED",
	})
}

// localizedRenderClipFields resolves the source-clip reference a localized
// render needs from a scene's clip bindings. It prefers the primary Clip and
// falls back to the first multi-clip binding; both are empty for audio-only
// scenes. The clip ID doubles as the media asset id (ClipReference.ID is the
// canonical asset identity) and DurationUS is converted to milliseconds.
func localizedRenderClipFields(scene Scene) (clipID, assetID, sha256 string, durationMS int64) {
	clip := scene.Clip
	if clip == nil && len(scene.Clips) > 0 {
		clip = scene.Clips[0]
	}
	if clip == nil {
		return "", "", "", 0
	}
	durationMS = clip.DurationUS / 1000
	if durationMS <= 0 && clip.Duration > 0 {
		durationMS = int64(clip.Duration * 1000)
	}
	return clip.ID, clip.ID, clip.SHA256, durationMS
}

func (r *Runner) SetCombinedAudioRenderer(renderer CombinedAudioRenderer) {
	r.combinedAudioRenderer = renderer
}

// SetCheckpointResolver wires the durable per-unit checkpoint resolver that
// gates unit reuse (resume) and records unit completions. Nil-safe; nil
// keeps the legacy best-effort idempotency path (restored partial result
// only, no artifact verification).
func (r *Runner) SetCheckpointResolver(resolver *capcheckpoint.Resolver) {
	if r != nil {
		r.checkpoints = resolver
	}
}

// SetAudioAssetSource wires the BGM/SFX asset_id → local path + certified
// duration resolver port. Nil-safe; nil means the audio intent block cannot
// be resolved and a run carrying BGM/SFX intents fails closed in the
// audio-compile phase.
func (r *Runner) SetAudioAssetSource(source AudioAssetSource) {
	if r != nil {
		r.audioAssetSource = source
	}
}

// SetFinalAudioPublisher wires the canonical delivery publisher used to make
// the certified full-audio Drive link available to the document phase.
func (r *Runner) SetFinalAudioPublisher(publisher FinalAudioPublisher) {
	if r != nil {
		r.finalAudioPublisher = publisher
	}
}

// SetScriptPersistence wires the canonical SQLite script-row writer. The
// runner invokes it only when GenerateRequest.SaveToDB is true.
func (r *Runner) SetScriptPersistence(persistence ScriptPersistence) {
	if r != nil {
		r.scriptPersistence = persistence
	}
}

// SetExecutionRecorder injects the durable execution/lineage port. A nil
// recorder restores the safe no-op implementation used by unit runtimes.
func (r *Runner) SetExecutionRecorder(recorder ExecutionRecorder) {
	if r != nil {
		r.setRecorder(recorder)
	}
}

// SetSceneCommitObserver wires the observer notified of every SceneCommitted
// event. A nil observer is safe and disables emission; a non-nil observer is
// fail-closed (a commit error fails the scene-text stage). This is the
// injection seam used by tests; per-run wiring registered by beginVidRush
// takes precedence, so concurrent runs never share a coordinator.
func (r *Runner) SetSceneCommitObserver(observer SceneCommitObserver) {
	if r != nil {
		r.sceneCommitObserver = observer
	}
}

// SetVidRushBarrier wires the final barrier awaited after scene generation
// completes. A nil barrier is safe and skips the wait; a non-nil barrier is
// fail-closed (a barrier error fails the run) and blocks only for enrichments
// still running, never re-running the whole-document EntitiesProcessor. This
// is the injection seam used by tests; per-run wiring registered by
// beginVidRush takes precedence.
func (r *Runner) SetVidRushBarrier(barrier VidRushBarrier) {
	if r != nil {
		r.vidRushBarrier = barrier
	}
}

// SetGenerationGate wires the capacity-bounded priority gate shared with
// VidRush entity extraction. Scene generation acquires it with high priority,
// so when the text generator and the entity extractor share the same local
// Ollama model, generation preempts extraction instead of queuing behind it.
// The gate capacity should match the NLP concurrency (DefaultNLPConcurrency).
func (r *Runner) SetGenerationGate(gate *GenerationGate) {
	if r != nil {
		r.generationGate = gate
	}
}

// SetTTSConcurrency sets the TTS voiceover worker-pool size. Values <= 0
// fall back to the certified default (DefaultTTSConcurrency).
func (r *Runner) SetTTSConcurrency(concurrency int) {
	if r == nil {
		return
	}
	if concurrency <= 0 {
		concurrency = DefaultTTSConcurrency
	}
	r.ttsConcurrency = concurrency
}

// SetTranslationConcurrency sets the bounded translation worker-pool size.
func (r *Runner) SetTranslationConcurrency(concurrency int) {
	if r == nil {
		return
	}
	if concurrency <= 0 {
		concurrency = DefaultTranslationConcurrency
	}
	r.translationConcurrency = concurrency
}

// SetVidRushTimingRecorder wires the recorder that receives the scene-
// generation wall-clock window, enabling the generation↔VidRush overlap
// metric. A nil recorder is safe and disables timing. This is the injection
// seam used by tests; per-run wiring registered by beginVidRush takes
// precedence.
func (r *Runner) SetVidRushTimingRecorder(recorder VidRushTimingRecorder) {
	if r != nil {
		r.vidRushTiming = recorder
	}
}

// SetVidRushPipeline wires the composition-time incremental VidRush
// dependencies. A nil pipeline disables incremental VidRush (batch workflows
// keep enriching the whole document later). The Runner builds a fresh,
// run-scoped coordinator from these dependencies for each run.
func (r *Runner) SetVidRushPipeline(pipeline *VidRushPipeline) {
	if r != nil {
		r.vidRushPipeline = pipeline
	}
}

// SetImageSearchResolver wires the deterministic Image Search Intent
// resolver (capabilities/imagesearch) into the run. It is the same resolver
// the golden battery certifies; the composition root builds it over the same
// entity extractor the VidRush pipeline uses. Nil keeps the legacy ad-hoc
// query builders as the fallback.
func (r *Runner) SetImageSearchResolver(resolver *capabilityimagesearch.Resolver) {
	if r != nil {
		r.imageSearchResolver = resolver
	}
}

// beginVidRush builds and registers a fresh, run-scoped
// VidRushIncrementalCoordinator when a VidRushPipeline is configured. It
// resolves the per-run plan, constructs the coordinator, and registers it in
// the per-run registry as the scene-commit observer, final barrier, and timing
// recorder. It returns a nil coordinator when VidRush is disabled. Registration
// under the run ID is what isolates concurrent runs: each run resolves its own
// wiring and never observes another run's coordinator.
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
	coordinator.SetGenerationGate(r.generationGate)

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
	coordinator, err := r.beginVidRush(ctx, runID, req)
	if err != nil {
		r.failRunWithRetry(ctx, runID, StageNormalizing, err)
		return
	}
	if coordinator != nil {
		defer r.endVidRush(runID)
	}
	var result *GenerateResult
	if !r.measurePhase(ctx, kernobs.StageGenerate, func(c context.Context) bool {
		var ok bool
		result, ok = r.runSceneTextPhase(c, runID, req, routing, exec, run, resumeIdx)
		return ok
	}) {
		return
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
			res, err := r.runVidRushJoinAndPrepare(prepareCtx, runID, req, snapshot)
			prepareDone <- vidRushPrepareOutcome{result: res, skeletons: skel, err: err}
		}()

		// TTS runs in the main goroutine, in parallel with the prepare branch.
		if !r.measurePhase(ctx, kernobs.StageName(voiceoverStage), func(c context.Context) bool {
			return r.runVoiceoverPhase(c, runID, req, routing, exec, resumeIdx, result)
		}) {
			// TTS failed: the deferred cancelPrepare stops the prepare branch.
			return
		}

		// Join the prepare branch; an error fails the run (fail-closed).
		outcome := <-prepareDone
		if outcome.err != nil {
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, outcome.err)
			return
		}
		applyVidRushPrepareProjections(result, outcome.result)
		skeletons = outcome.skeletons
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
