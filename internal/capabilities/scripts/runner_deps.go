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

	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"
	capabilityimagesearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/imagesearch"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"go.uber.org/zap"
)

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

// SetVoiceoverPublishDrainer wires the async voiceover publish pool
// (P0.4: separate TTS pool from publish pool). After the voiceover
// phase, the runner drains the pool so Drive links are hydrated before
// downstream stages (audio compile, docs) consume them. A nil drainer
// means synchronous publish (backward compat).
func (r *Runner) SetVoiceoverPublishDrainer(drainer interface{ Wait() }) {
	if r != nil {
		r.voiceoverPublishDrainer = drainer
	}
}

// SetMediaPreflight wires the fail-fast media requirement verification
// (P0.5). When wired, the runner fires the preflight in parallel with
// Gemma and fails the run at the join point if any check failed. Nil
// means the preflight is skipped (backward compat).
func (r *Runner) SetMediaPreflight(preflight MediaPreflight) {
	if r != nil {
		r.mediaPreflight = preflight
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
	// The localization artifact is certified by its Drive identity, while
	// older clip-render paths also provide a registry AssetID. Do not discard
	// a successfully uploaded MP4 merely because the localization adapter did
	// not mint a second registry id.
	if strings.TrimSpace(rendered.AssetID) == "" && strings.TrimSpace(rendered.DriveFileID) == "" && strings.TrimSpace(rendered.DriveLink) == "" {
		return nil
	}
	if strings.TrimSpace(rendered.AssetID) == "" {
		rendered.AssetID = "drive:" + strings.TrimSpace(rendered.DriveFileID)
	}
	if result != nil {
		r.localizedRenderMu.Lock()
		applyLocalizedRenderLinkLocked(result, rendered)
		result.LocalizedRenders = append(result.LocalizedRenders, rendered)
		accumulateLocalizedRenderMetrics(result, rendered)
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

// accumulateLocalizedRenderMetrics records both child work and the enclosing
// fan-out wall span. Child durations are summed as WorkMS; the parent wall is
// first-start to last-finish and therefore remains correct under concurrency.
func accumulateLocalizedRenderMetrics(result *GenerateResult, rendered LocalizedRenderResult) {
	if result == nil || result.RenderMetrics == nil {
		return
	}
	if rendered.WallMS > 0 {
		result.RenderMetrics.WorkMS += rendered.WallMS
	}
	if !rendered.StartedAt.IsZero() && (result.renderFirstStartedAt.IsZero() || rendered.StartedAt.Before(result.renderFirstStartedAt)) {
		result.renderFirstStartedAt = rendered.StartedAt
	}
	if !rendered.FinishedAt.IsZero() && rendered.FinishedAt.After(result.renderLastFinishedAt) {
		result.renderLastFinishedAt = rendered.FinishedAt
	}
	if !result.renderFirstStartedAt.IsZero() && !result.renderLastFinishedAt.IsZero() {
		result.RenderMetrics.WallMS = result.renderLastFinishedAt.Sub(result.renderFirstStartedAt).Milliseconds()
	}
}

// applyLocalizedRenderLinkLocked replaces the source Drive link in the
// document-facing clip reference with the certified rendered artifact link.
// The source link is still retained by the asset registry; a generated script
// must point at the output produced by this run.
func applyLocalizedRenderLinkLocked(result *GenerateResult, rendered LocalizedRenderResult) {
	if result == nil || strings.TrimSpace(rendered.DriveLink) == "" {
		return
	}
	for _, scene := range result.Scenes {
		// A scene can contain several intro clips, while the renderer reports
		// one certified artifact per clip on its own scene.  Match by canonical
		// clip ID across the whole script so every occurrence in the document
		// points at the regenerated MP4, including repeated intro bindings.
		for _, clip := range append(append([]*ClipReference{}, scene.Clips...), scene.Clip) {
			if clip != nil && clip.ID == rendered.ClipID {
				clip.DriveLink = rendered.DriveLink
			}
		}
	}
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

func (r *Runner) SetFinalVideoAssembler(assembler FinalVideoAssembler) {
	if r != nil {
		r.finalVideoAssembler = assembler
	}
}

func (r *Runner) SetFinalVideoPublisher(publisher FinalVideoPublisher) {
	if r != nil {
		r.finalVideoPublisher = publisher
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

// SetGenerationGate wires the capacity-bounded gate for scene-text
// generation. Entity extraction uses its own gate via SetNLPGenerationGate.
func (r *Runner) SetGenerationGate(gate *GenerationGate) {
	if r != nil {
		r.generationGate = gate
	}
}

// SetNLPGenerationGate wires the independent gate used only by VidRush
// entity extraction. It must not be reused as the script-writing gate.
func (r *Runner) SetNLPGenerationGate(gate *GenerationGate) {
	if r != nil {
		r.nlpGenerationGate = gate
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
