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
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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

	// snapshot is the read-only scene-text projection taken before the
	// concurrent TTS and VidRush-prepare branches start.
	snapshot []sceneTextSnapshot

	// stockPrefetchDone carries the best-effort stock warm channel started at
	// run start. It is joined ONLY at the first consumer that can use warmed
	// stock assets (sceneTextReady), never before translate: translation needs
	// only scene text and must not wait for stock I/O.
	stockPrefetchDone chan scriptports.StockPrefetchReport
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

	// Resolve the canonical artifact routing context once before any I/O.
	routing, resolveErr := e.req.resolveArtifactRoutingContext()
	if resolveErr != nil {
		return e.fail(StagePublishingDocuments, resolveErr)
	}
	e.routing = routing

	// Determine resume stage from the existing run (if any).
	run, err := e.r.repo.Get(e.ctx, e.runID)
	e.resumeIdx = -1 // -1 means start from beginning
	if err == nil && run != nil {
		resumeStage := ResumeFrom(run)
		if run.Status == RunStatusFailed && run.FailedStage == StagePublishingDocuments && missingUnpublishedFinalAudio(run.Result) {
			// A publish-stage retry normally resumes at publication. That cannot
			// recover a final-audio file which disappeared from local storage:
			// the same stale path would be checked again. Re-enter audio compile,
			// discard only the lost local artifact reference, and keep the already
			// rendered overlay references in the durable result.
			resumeStage = StageCompilingAudio
			run.Result.FinalAudio = nil
			e.r.log.Warn("final audio artifact is missing; retrying from audio compile",
				zap.String("run_id", e.runID),
			)
		}
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

func missingUnpublishedFinalAudio(result *GenerateResult) bool {
	if result == nil || result.FinalAudio == nil || strings.TrimSpace(result.FinalAudio.DriveLink) != "" {
		return false
	}
	path := strings.TrimSpace(result.FinalAudio.Path)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return errors.Is(err, os.ErrNotExist)
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
	return e.measure(kernobs.StageRunMediaPreflight, func(c context.Context) bool {
		if stageSkipped(e.resumeIdx, StagePreflight) {
			if err := e.validateEnabledVidRushProviders(c); err != nil {
				return e.fail(StagePreflight, err)
			}
			return true
		}
		if err := e.r.updateStage(c, e.runID, RunStatusRunning, StagePreflight); err != nil {
			return e.fail(StagePreflight, err)
		}
		if e.r.mediaPreflight == nil {
			if err := e.validateEnabledVidRushProviders(c); err != nil {
				return e.fail(StagePreflight, err)
			}
			if e.req.Intro == nil && e.req.Outro == nil {
				return true
			}
			result := PreflightResult{Failures: []PreflightFailure{{
				Category: "fixed_media",
				Detail:   "media preflight is not wired — fixed media cannot be certified before generation",
			}}}
			return e.fail(StagePreflight, result.AsError())
		}
		if _, ok := e.r.mediaPreflight.(VidRushProviderAvailabilityPreflight); !ok {
			if err := e.validateEnabledVidRushProviders(c); err != nil {
				return e.fail(StagePreflight, err)
			}
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

func (e *executionRun) validateEnabledVidRushProviders(ctx context.Context) error {
	if availabilityPreflight, ok := e.r.mediaPreflight.(VidRushProviderAvailabilityPreflight); ok {
		return availabilityPreflight.RunVidRushProviderAvailability(ctx, e.req).AsError()
	}
	return RunVidRushProviderAvailabilityPreflight(ctx, e.req.MediaPlan, nil).AsError()
}

// beginVidRushPhase registers run-scoped VidRush wiring only after the
// synchronous media preflight has passed. The coordinator lifetime remains
// owned by the caller so concurrent runs stay isolated.
func (e *executionRun) beginVidRushPhase() bool {
	var beginVidRushErr error
	if err := kernobs.MeasureStage(e.ctx, kernobs.StageBeginVidRush, func(stageCtx context.Context) error {
		e.coordinator, beginVidRushErr = e.r.beginVidRush(stageCtx, e.runID, e.req)
		return beginVidRushErr
	}); err != nil && beginVidRushErr == nil {
		beginVidRushErr = err
	}
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
	// P0-A: first stock consumer joins the best-effort warm here (bounded),
	// so translate/generate never wait for stock I/O.
	e.joinStockPrefetchForConsumer()
	e.snapshot = snapshotSceneText(e.result.Scenes, e.req.SourceLanguage, e.req)

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
	// Freeze the fields translated NLP consumes before TTS starts mutating
	// scene structs. The semantic branch uses this detached projection while
	// voiceover workers update e.result.Scenes concurrently.
	nlpInput := localizedNLPSnapshot(e.result)
	prepareCtx, cancelPrepare := context.WithCancel(e.ctx)
	defer cancelPrepare()
	// Start semantic enrichment first and independently. Audio prefetch runs on
	// its own branch so it overlaps both NLP and TTS.
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
		localized, nlpErr := e.r.computeLocalizedAnnotations(prepareCtx, e.req, nlpInput, res.annotations)
		semanticDone <- vidRushPrepareOutcome{result: res, localizedAnnotations: localized, nlpErr: nlpErr}
	}()

	// Audio prefetch is independent of semantic enrichment; keep it on a
	// second branch so it also overlaps both NLP and TTS.
	assetsDone := make(chan vidRushPrepareOutcome, 1)
	go func() {
		// ── P1.1 Audio prefetch ──────────────────────────────
		// Resolve BGM/SFX assets and materialize original clip audio in
		// parallel with TTS. Best-effort: skip when the source is nil.
		var prefetched *AudioPrefetchResult
		if e.r.audioAssetSource != nil && (len(e.req.BackgroundMusic) > 0 || len(e.req.SoundEffects) > 0 || e.req.RandomSFXOnClipStart ||
			e.req.MixPolicy.Normalize() == capabilityaudio.MixVoiceoverWithDuckedClip) {
			bgmIDs := make([]string, len(e.req.BackgroundMusic))
			for i, b := range e.req.BackgroundMusic {
				bgmIDs[i] = b.AssetID
			}
			sfxIDs := make([]string, len(e.req.SoundEffects))
			for i, s := range e.req.SoundEffects {
				sfxIDs[i] = s.AssetID
			}
			if e.req.RandomSFXOnClipStart {
				clipStartTimeline, timelineErr := CompileCanonicalTimeline(*e.result)
				if timelineErr != nil {
					e.r.log.Warn("clip-start SFX prefetch timeline unavailable; compile will resolve assets synchronously", zap.String("run_id", e.runID), zap.Error(timelineErr))
				} else {
					for _, intent := range randomClipStartSFXIntents(clipStartTimeline) {
						sfxIDs = append(sfxIDs, intent.AssetID)
					}
				}
			}
			var clipIDs []string
			if !e.req.FinalJob {
				for _, s := range e.snapshot {
					clipIDs = append(clipIDs, s.ClipIDs...)
				}
			}
			var clipAudioSource ClipAudioAssetSource
			if candidate, ok := e.r.audioAssetSource.(ClipAudioAssetSource); ok {
				clipAudioSource = candidate
			}
			// The prefetch is BOUNDED by its own budget: it overlaps TTS and
			// the render fan-out, but a stalled asset resolution must never
			// delay the join (and therefore the audio stage) indefinitely.
			// Whatever is cached when the budget expires is kept; the
			// remainder is resolved synchronously by audio compile through
			// the same cache adapters (their fall-through path).
			prefetchCtx, cancelPrefetch := context.WithTimeout(prepareCtx, audioPrefetchBudget)
			pf, pfErr := PrefetchAudioAssets(prefetchCtx, bgmIDs, sfxIDs, e.r.audioAssetSource, clipIDs, clipAudioSource, e.req.MixPolicy)
			cancelPrefetch()
			switch {
			case pfErr != nil:
				e.r.log.Warn("audio prefetch not attempted — audio compile will run with synchronous resolution",
					zap.String("run_id", e.runID),
					zap.Error(pfErr))
			case pf.Degraded:
				// Partial success is kept: the cached assets are served from
				// memory and only the missing ones fall through to I/O.
				prefetched = pf
				e.r.log.Warn("audio prefetch degraded — part of the audio I/O will be resolved synchronously",
					zap.String("run_id", e.runID),
					zap.Int("requested", pf.RequestedCount()),
					zap.Int("cached", pf.CachedCount()),
					zap.Int64("duration_ms", pf.DurationMS),
					zap.Strings("failures", pf.Failures))
			default:
				prefetched = pf
				e.r.log.Info("audio prefetch complete",
					zap.String("run_id", e.runID),
					zap.Int("requested", pf.RequestedCount()),
					zap.Int("cached", pf.CachedCount()),
					zap.Int64("duration_ms", pf.DurationMS))
			}
		}
		assetsDone <- vidRushPrepareOutcome{
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
	if err := kernobs.MeasureStage(e.ctx, "prepare_join", func(stageCtx context.Context) error {
		semanticOutcome = <-semanticDone
		assetsOutcome = <-assetsDone
		return semanticOutcome.err
	}); err != nil && semanticOutcome.err == nil {
		semanticOutcome.err = err
	}
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
	// Store the prefetched audio assets so the audio-compile phase consumes
	// them without blocking on I/O.
	e.result.AudioPrefetch = assetsOutcome.prefetched
	e.checkpoint()
	return true
}

func (e *executionRun) finalJob() bool {
	if !e.req.FinalJob {
		return true
	}
	return e.measure(kernobs.StageRunFinalJob, func(c context.Context) bool {
		return e.r.submitFinalJob(c, e.runID, e.req, e.result)
	})
}

// persist stores the canonical script. An explicitly requested remote final
// render runs after persistence and before document publication.
func (e *executionRun) persist() bool {
	if !e.measure(kernobs.StageName(stagePersistence), func(c context.Context) bool {
		e.checkpoint()
		return e.r.persistScript(c, e.runID, e.req, e.exec, e.resumeIdx, e.result)
	}) {
		return false
	}
	return true
}

// complete finalizes a successful run (render-set certification, critical-path
// summary, pipeline invariants) and marks it COMPLETED.
func (e *executionRun) complete() {
	// Join the async voiceover publish pool on EVERY path, including runs
	// with docs removed: the published links are consumed by the cross-run
	// voiceover cache and the media registry, and a run that completes while
	// its uploads are still in flight orphans them past its own lifecycle
	// (K2). The uploads themselves are deliberately not gated on documents.
	if e.r.voiceoverPublishDrainer != nil {
		publishDrainStarted := time.Now()
		e.r.voiceoverPublishDrainer.Wait()
		voiceoverGenerated := 0
		if e.result != nil && e.result.AudioMetrics != nil {
			voiceoverGenerated = e.result.AudioMetrics.VoiceoverGenerated
		}
		kernobs.RecordStage(e.ctx, kernobs.StageInfo{Stage: "publish_pool_drain", ItemsInput: int64(voiceoverGenerated)}, publishDrainStarted, time.Now(), nil)
	}
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
