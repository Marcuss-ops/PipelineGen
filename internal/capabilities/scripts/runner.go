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
	"sync"
	"time"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

func sourceTraceFromResult(result *GenerateResult) scriptpkg.SourceTrace {
	trace := scriptpkg.SourceTrace{}
	if result == nil {
		return trace
	}
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
	scriptPersistence     ScriptPersistence
	recorder              ExecutionRecorder
	sceneCommitObserver   SceneCommitObserver
	vidRushBarrier        VidRushBarrier
	vidRushTiming         VidRushTimingRecorder
	generationGate        *GenerationGate
	vidRushPipeline       *VidRushPipeline

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
		repo:             repo,
		textGen:          textGen,
		translator:       translator,
		voiceoverGen:     voiceoverGen,
		docPublisher:     docPublisher,
		documentRenderer: documentRenderer,
		recorder:         noopExecutionRecorder{},
		vidRushRuns:      make(map[string]vidRushWiring),
		log:              zap.NewNop(),
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

func (r *Runner) SetCombinedAudioRenderer(renderer CombinedAudioRenderer) {
	r.combinedAudioRenderer = renderer
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

// SetGenerationGate wires the single-slot priority gate shared with VidRush
// entity extraction. Scene generation acquires it with high priority, so when
// the text generator and the entity extractor share the same local Ollama
// model, generation preempts extraction instead of queuing behind it.
func (r *Runner) SetGenerationGate(gate *GenerationGate) {
	if r != nil {
		r.generationGate = gate
	}
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

// beginVidRush builds and registers a fresh, run-scoped
// VidRushIncrementalCoordinator when a VidRushPipeline is configured. It
// resolves the per-run plan, constructs the coordinator, and registers it in
// the per-run registry as the scene-commit observer, final barrier, and timing
// recorder. It returns a nil coordinator when VidRush is disabled. Registration
// under the run ID is what isolates concurrent runs: each run resolves its own
// wiring and never observes another run's coordinator.
func (r *Runner) beginVidRush(ctx context.Context, runID string, req GenerateRequest) (*VidRushIncrementalCoordinator, error) {
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
	coordinator := NewVidRushIncrementalCoordinatorWithBackpressure(p.Enricher, plan, p.Backpressure)
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
		result, ok = r.runSceneTextPhase(c, runID, req, exec, run, resumeIdx)
		return ok
	}) {
		return
	}
	if result == nil {
		result = &GenerateResult{Scenes: []Scene{}, Title: req.Title, OutputName: req.OutputName, VoiceoverGroup: req.ScriptParams.VoiceoverGroup}
	}
	// Final VidRush barrier: wait only for enrichments still running, never
	// re-running the whole-document EntitiesProcessor. The fenced per-scene
	// results are then projected onto the durable result so a SUCCEEDED run
	// exposes its typed entity aggregate (persons/places/concepts) on the
	// surface — never a silent no-op for a backend that did run.
	segments, err := r.waitForVidRush(ctx, runID)
	if err != nil {
		r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, err)
		return
	}
	if len(segments) > 0 {
		result.Entities = aggregateEntityResult(segments)
		// Project each segment's entities onto its scene's annotations so the
		// document SpecScene and the /full surface carry the same grounded
		// per-scene entity proof (primary/secondary), never just the flat
		// aggregate.
		applySegmentEntityAnnotations(result, req.SourceLanguage, segments)
		// Project each segment's typed entities onto its scene's canonical
		// per-scene EntityResult (the same model as the document aggregate —
		// no second entity model), and derive entity_overlay_required. A
		// scene with no entities keeps entities=[] + entity_overlay_required=
		// false; no entity is invented.
		applySegmentEntityResults(result, segments)
	}
	projectEntityCompatibility(result, segments)
	// ── OVERLAY INTENT PLANNING ──────────────────────────────────────
	// Create OverlayIntents from per-scene entity annotations immediately
	// after extraction. This runs BEFORE TTS so overlay.prepare can start
	// template resolution and asset prefetch in parallel with audio synthesis.
	if r.overlayRegistry != nil {
		result.OverlayIntents = planOverlayIntents(result.Scenes, r.overlayRegistry)
	}
	result.SourceTrace = sourceTraceFromResult(result)
	if !r.measurePhase(ctx, kernobs.StageName(stageTranslation), func(c context.Context) bool {
		return r.runTranslationPhase(c, runID, req, exec, resumeIdx, result)
	}) {
		return
	}
	if !r.measurePhase(ctx, kernobs.StageName(voiceoverStage), func(c context.Context) bool {
		return r.runVoiceoverPhase(c, runID, req, routing, exec, resumeIdx, result)
	}) {
		return
	}
	if !r.measurePhase(ctx, kernobs.StageName(audioCompileStage), func(c context.Context) bool {
		if !r.runAudioCompilePhase(c, runID, req, exec, resumeIdx, result) {
			return false
		}
		return r.publishFinalAudio(c, runID, req, routing, result)
	}) {
		return
	}
	if !r.measurePhase(ctx, kernobs.StageName(stagePersistence), func(c context.Context) bool {
		return r.persistScript(c, runID, req, exec, resumeIdx, result)
	}) {
		return
	}
	if !r.measurePhase(ctx, kernobs.StageName(stageDocument), func(c context.Context) bool {
		return r.runDocumentPhase(c, runID, req, routing, exec, resumeIdx, result)
	}) {
		return
	}
	r.completeRun(ctx, runID, result)
}
