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

// OverlayRenderEnqueuer returns the configured Chronon queue boundary so the
// legacy batch child path can share the exact same publisher, fresh-render
// policy and queue client as the durable runner. It is read-only wiring; the
// runner remains the owner of the field.
func (r *Runner) OverlayRenderEnqueuer() OverlayRenderEnqueuer {
	if r == nil {
		return nil
	}
	return r.overlayRenderEnqueuer
}

// SetLocalizedRenderEnqueuer wires the per-(scene, language) localized render
// fan-out. A nil enqueuer disables the fan-out (render not registered); a
// non-nil enqueuer is fail-closed (an enqueue error fails the run).
func (r *Runner) SetLocalizedRenderEnqueuer(enqueuer LocalizedRenderEnqueuer) {
	if r != nil {
		r.localizedRenderEnqueuer = enqueuer
	}
}

// SetDocumentFolderResolver wires the folder authority that resolves the
// per-language run folder each script document publishes into
// (<documents root>/<job>/<language>), so one language's document and its clips
// share a folder. A nil resolver keeps the historical flat documents root.
func (r *Runner) SetDocumentFolderResolver(resolver DocumentFolderResolver) {
	if r != nil {
		r.documentFolderResolver = resolver
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

// SetOverlayPublicationDrainer wires the bounded post-render publication pool.
// The runner joins it immediately before completion so Drive failures remain
// terminal even though they no longer occupy a RenderingGen render slot.
// The drainer is handed the run's context, because the pool is process-wide
// while the join must be scoped to the run that queued the work.
func (r *Runner) SetOverlayPublicationDrainer(drainer interface{ Wait(context.Context) error }) {
	if r != nil {
		r.overlayPublicationDrainer = drainer
	}
}

// SetMediaPreflight wires the fail-fast media requirement verification
// (P0.5). When wired, the runner executes it synchronously after normalize
// and before Gemma; any failure aborts the run before LLM, translation, or
// TTS. Fixed-media requests fail closed when it is not wired.
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
	// A nil enqueuer is the supported hermetic/degraded composition: the
	// runner can still certify script, translation and TTS without claiming a
	// video render. Production wiring installs the Chronon adapter; failures
	// from that non-nil adapter remain fail-closed below.
	if r.localizedRenderEnqueuer == nil {
		return nil
	}
	if input.Voiceover.ID == "" && strings.TrimSpace(input.ClipAssetID) == "" && strings.TrimSpace(input.ClipID) == "" {
		return nil
	}
	if input.ResumeFrom != nil {
		if recovery, ok := r.localizedRenderEnqueuer.(LocalizedRenderRecoveryEnqueuer); ok {
			return recovery.UploadRendered(ctx, input, *input.ResumeFrom)
		}
		return fmt.Errorf("localized render recovery requested but upload-only adapter is not wired")
	}
	return r.localizedRenderEnqueuer.EnqueueLocalizedRender(ctx, input)
}

func (r *Runner) stagedLocalizedRender(result *GenerateResult, sceneID string, lang Language, clipID string) *LocalizedRenderResult {
	if result == nil {
		return nil
	}
	r.localizedRenderMu.Lock()
	defer r.localizedRenderMu.Unlock()
	for i := range result.LocalizedRenderStaged {
		v := &result.LocalizedRenderStaged[i]
		if v.SceneID == sceneID && v.Language == lang && v.ClipID == clipID && strings.EqualFold(v.Status, "RENDERED") {
			copy := *v
			return &copy
		}
	}
	return nil
}

func (r *Runner) recordLocalizedRenderReady(ctx context.Context, exec ExecutionContext, result *GenerateResult, rendered LocalizedRenderResult) error {
	if result == nil || strings.TrimSpace(rendered.LocalPath) == "" || strings.TrimSpace(rendered.SHA256) == "" {
		return fmt.Errorf("localized render ready: local path and sha256 are required")
	}
	// Snapshot under lock, release before I/O.
	r.localizedRenderMu.Lock()
	found := false
	for i := range result.LocalizedRenderStaged {
		v := &result.LocalizedRenderStaged[i]
		if v.SceneID == rendered.SceneID && v.Language == rendered.Language && v.ClipID == rendered.ClipID {
			*v = rendered
			found = true
			break
		}
	}
	if !found {
		result.LocalizedRenderStaged = append(result.LocalizedRenderStaged, rendered)
	}
	// The render itself is certified (only its publication is pending), so the
	// parent-visible `render` stage is reported completed here too: the upload
	// stage separately owns the publication that has not happened yet.
	recordRenderStageProgress(result, rendered)
	// Copy for checkpoint outside lock.
	snapshot := *result
	r.localizedRenderMu.Unlock()

	if err := r.repo.SavePartialResult(ctx, exec.JobID, &snapshot); err != nil {
		if found {
			return fmt.Errorf("localized render ready: checkpoint update: %w", err)
		}
		return fmt.Errorf("localized render ready: checkpoint save: %w", err)
	}
	r.log.Info("localized render staged", zap.String("job_id", exec.JobID), zap.String("scene_id", rendered.SceneID), zap.String("language", string(rendered.Language)), zap.String("clip_id", rendered.ClipID), zap.String("sha256", rendered.SHA256))
	return nil
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
	// Snapshot in-memory mutation under lock, release before durable I/O.
	var snapshot *GenerateResult
	if result != nil {
		r.localizedRenderMu.Lock()
		removeStagedLocalizedRenderLocked(result, rendered)
		result.LocalizedRenders = append(result.LocalizedRenders, rendered)
		accumulateLocalizedRenderMetrics(result, rendered)
		recordRenderStageProgress(result, rendered)
		snap := *result
		snapshot = &snap
		r.localizedRenderMu.Unlock()
	}
	// Durable lineage: the produced video is an OperationRender artifact
	// joinable on (scene_id, language, asset_id) like every other produced
	// artifact of the run. No lock held — recorder serialises internally.
	if err := r.recordArtifactOperation(ctx, exec, ArtifactOperation{
		// A fixed intro/outro can fan out several source clips under the
		// same scene and language. ClipID is part of the operation identity
		// so each produced MP4 has its own durable lineage row.
		OperationID: artifactOperationID(exec.Attempt, OperationRender, rendered.SceneID, string(rendered.Language), rendered.ClipID),
		Kind:        OperationRender,
		SceneID:     rendered.SceneID,
		Language:    rendered.Language,
		AssetID:     rendered.AssetID,
		Status:      "COMPLETED",
	}); err != nil {
		return err
	}
	// Persist immediately after each certified unit, not only when the whole
	// fan-out joins. This makes a successful render/upload visible to resume
	// after a process crash in a later sibling. Uses snapshot so no lock held.
	if snapshot != nil {
		r.repo.SavePartialResult(ctx, exec.JobID, snapshot)
	}
	return nil
}

func removeStagedLocalizedRenderLocked(result *GenerateResult, rendered LocalizedRenderResult) {
	if result == nil {
		return
	}
	for i := range result.LocalizedRenderStaged {
		v := result.LocalizedRenderStaged[i]
		if v.SceneID == rendered.SceneID && v.Language == rendered.Language && v.ClipID == rendered.ClipID {
			result.LocalizedRenderStaged = append(result.LocalizedRenderStaged[:i], result.LocalizedRenderStaged[i+1:]...)
			return
		}
	}
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

// applyLocalizedRenderLinkLocked projects one certified render onto the shared
// scene clip reference, but ONLY when that render is the run's
// SOURCE-language variant of the clip.
//
// The scene clip reference IS the source clip: one video, one language. A run
// renders one variant per (clip, language), so the previous "the newest render
// wins" rule made this language-less field carry whichever language finished
// last — and every language's document that fell back to this field showed one
// arbitrary, usually foreign, video. The per-language links are carried by
// GenerateResult.LocalizedRenders and read per language by the document
// projection (localizedRenderLinksFor); only the source-language render
// legitimately replaces the source clip's own link.
//
// A run whose source language is unknown (restored legacy checkpoints) keeps
// the accept-any-render behaviour so those results are not silently dropped.
func applyLocalizedRenderLinkLocked(result *GenerateResult, rendered LocalizedRenderResult) {
	if result == nil || strings.TrimSpace(rendered.DriveLink) == "" {
		return
	}
	if source := strings.TrimSpace(string(result.SourceLanguage)); source != "" &&
		strings.TrimSpace(string(rendered.Language)) != source {
		// A translated variant of this clip is a different deliverable and
		// must never overwrite the source clip's identity in the scene graph.
		return
	}
	for _, scene := range result.Scenes {
		if scene.Clip != nil && scene.Clip.ID == rendered.ClipID {
			scene.Clip.DriveLink = rendered.DriveLink
		}
		for _, clip := range scene.Clips {
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
	if durationMS <= 0 && clip.SourceOutMS > clip.SourceInMS {
		durationMS = clip.SourceOutMS - clip.SourceInMS
	}
	if durationMS <= 0 && scene.DurationMS > 0 {
		durationMS = scene.DurationMS
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

// SetOverlayBackgroundSource wires the catalog/cache resolver used for
// visual background asset_ids. Nil keeps color backgrounds and callers that
// already provide a complete content-addressed ref compatible.
func (r *Runner) SetOverlayBackgroundSource(source OverlayBackgroundSource) {
	if r != nil {
		r.overlayBackgroundSource = source
	}
}

// OverlayBackgroundSource returns the canonical background resolver so the
// batch child path can use the same catalog/cache boundary as the durable
// runner. The returned port is read-only and nil when not wired.
func (r *Runner) OverlayBackgroundSource() OverlayBackgroundSource {
	if r == nil {
		return nil
	}
	return r.overlayBackgroundSource
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
// still running, never re-running whole-document extraction. This
// is the injection seam used by tests; per-run wiring registered by
// beginVidRush takes precedence.
func (r *Runner) SetVidRushBarrier(barrier VidRushBarrier) {
	if r != nil {
		r.vidRushBarrier = barrier
	}
}

// SetNLPGenerationGate wires the independent gate used only by VidRush
// entity extraction. It must not be reused as the script-writing gate: the
// scene-text generation gate belongs to the generator engine
// (Engine.SetGenerationGate), which owns the per-call limit.
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

// SetOverlayRenderConcurrency sets the multilingual overlay render fan-out
// width: how many per-language OverlayPlans the overlay_render phase may have
// in flight against RenderingGen at once. Values <= 0 fall back to the
// certified default (DefaultOverlayRenderConcurrency). The effective width is
// additionally clamped to the number of plans that still need rendering, so a
// single-language run never pays for an idle slot.
//
// It is a PIPELINING bound, not a GPU bound: RenderingGen's worker owns
// worker.gpu_lanes and stays the only authority on concurrent GPU work.
func (r *Runner) SetOverlayRenderConcurrency(concurrency int) {
	if r == nil {
		return
	}
	if concurrency <= 0 {
		concurrency = DefaultOverlayRenderConcurrency
	}
	r.overlayRenderConcurrency = concurrency
}

// overlayRenderWorkers resolves the effective overlay render fan-out width,
// never returning less than 1 so at least one plan is always submitted.
func (r *Runner) overlayRenderWorkers() int {
	if r == nil || r.overlayRenderConcurrency <= 0 {
		return DefaultOverlayRenderConcurrency
	}
	return r.overlayRenderConcurrency
}

// OverlayRenderConcurrency reports the effective overlay render fan-out width
// (the certified default when unset). It is read-only wiring: the runner remains
// the owner of the field, and this exists so the composition root can log the
// width it actually wired instead of re-deriving it.
func (r *Runner) OverlayRenderConcurrency() int {
	return r.overlayRenderWorkers()
}

// SetTranslationConcurrency sets the bounded translation worker-pool size.
func (r *Runner) SetTranslationConcurrency(concurrency int) {
	if r == nil {
		return
	}
	if concurrency <= 0 {
		concurrency = DefaultTranslationConcurrency
	}
	if concurrency > MaxTranslationConcurrency {
		concurrency = MaxTranslationConcurrency
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
