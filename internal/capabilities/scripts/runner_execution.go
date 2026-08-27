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
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
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

	// preflight is the P0.5 media-preflight result channel started AFTER
	// normalize and joined AFTER the generate phase. Nil means preflight is
	// not wired (backward compat).
	preflight <-chan PreflightResult

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

// beginMediaPreflight starts the P0.5 fail-fast media verification in a
// goroutine (it runs in parallel with scene-text generation). Returns false
// when VidRush wiring fails (terminal). The VidRush coordinator lifetime is
// owned by the caller (runExecution) so it is released only after all phases
// that consume the wiring complete.
func (e *executionRun) beginMediaPreflight() bool {
	// VidRush wiring is a setup boundary: registering the run's coordinator
	// must happen before the fan-out. Its timing is owned by the wrapper.
	var beginVidRushErr error
	kernobs.MeasureStage(e.ctx, "begin_vidrush", func(stageCtx context.Context) error {
		e.coordinator, beginVidRushErr = e.r.beginVidRush(stageCtx, e.runID, e.req)
		return beginVidRushErr
	})
	if beginVidRushErr != nil {
		return e.fail(StageNormalizing, beginVidRushErr)
	}

	// P0.5 Media Preflight (parallel with Gemma). Start the fail-fast asset
	// verification; join after the generate phase.
	if e.r.mediaPreflight != nil {
		pfCh := make(chan PreflightResult, 1)
		e.preflight = pfCh
		go func() {
			pfCh <- e.r.mediaPreflight.Run(e.ctx, e.req)
		}()
	}
	return true
}

// generate runs scene-text generation (the Gemma phase) and records the
// generate-phase KPI milestones. It must run after media preflight started.
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

// joinPreflight joins the media-preflight goroutine (if started) and fails
// the run fail-closed on any asset-verification failure BEFORE any TTS work.
func (e *executionRun) joinPreflight() bool {
	if e.preflight == nil {
		return true
	}
	preflightJoinStarted := time.Now()
	pfResult := <-e.preflight
	if pfResult.HasFailures() {
		kernobs.RecordStage(e.ctx, kernobs.StageInfo{Stage: "media_preflight_join"}, preflightJoinStarted, time.Now(), fmt.Errorf("preflight: %d failures", len(pfResult.Failures)))
	} else {
		kernobs.RecordStage(e.ctx, kernobs.StageInfo{Stage: "media_preflight_join"}, preflightJoinStarted, time.Now(), nil)
	}
	e.r.log.Info("media preflight completed",
		zap.String("run_id", e.runID),
		zap.Int("failures", len(pfResult.Failures)),
		zap.Int64("wall_ms", pfResult.WallMS))
	if pfResult.HasFailures() {
		e.r.log.Warn("media preflight FAILED — run aborted before TTS",
			zap.String("run_id", e.runID),
			zap.String("failures", pfResult.Error()))
		return e.fail(StagePreflight, fmt.Errorf("media preflight: %s", pfResult.Error()))
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

// sceneTextReady runs the SceneTextReady fan-out: the VidRush join +
// overlay.prepare branch either serially (serialMode, "before" baseline) or
// in parallel with TTS (the production DAG). It owns the prepare-branch
// concurrency lifecycle (cancellation, join, projection) and the checkpoint
// after the fan-out. Returns false on a terminal error.
func (e *executionRun) sceneTextReady() bool {
	e.snapshot = snapshotSceneText(e.result.Scenes, e.req.SourceLanguage)

	ok := false
	if e.r.serialMode {
		ok = e.serialFanOut()
	} else {
		ok = e.parallelFanOut()
	}
	if ok {
		e.result.SourceTrace = sourceTraceFromResult(e.result)
	}
	return ok
}

// serialFanOut reproduces the pre-parallel "before" chain: entities → voiceover.
// The VidRush join + overlay.prepare runs blocking first, then TTS starts.
func (e *executionRun) serialFanOut() bool {
	prepared, err := e.r.runVidRushJoinAndPrepare(e.ctx, e.runID, e.req, e.snapshot)
	if err != nil {
		return e.fail(StageGeneratingSceneText, err)
	}
	applyVidRushPrepareProjections(e.result, prepared)

	ok := e.measure(kernobs.StageName(voiceoverStage), func(c context.Context) bool {
		return e.r.runVoiceoverPhase(c, e.runID, e.req, e.routing, e.exec, e.resumeIdx, e.result)
	})
	if !ok {
		return false
	}
	e.checkpoint()
	return true
}

// parallelFanOut runs the production SceneTextReady DAG: the VidRush join +
// overlay.prepare branch (DocsPrepare + audio prefetch) runs concurrently
// with TTS, then the branch is joined and its projections applied.
func (e *executionRun) parallelFanOut() bool {
	prepareCtx, cancelPrepare := context.WithCancel(e.ctx)
	defer cancelPrepare()
	prepareDone := make(chan vidRushPrepareOutcome, 1)
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
			for _, s := range e.result.Scenes {
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

		res, err := e.r.runVidRushJoinAndPrepare(prepareCtx, e.runID, e.req, e.snapshot)
		prepareDone <- vidRushPrepareOutcome{
			result:     res,
			skeletons:  skel,
			prefetched: prefetched,
			err:        err,
		}
	}()

	// TTS runs in the main goroutine, in parallel with the prepare branch.
	if !e.measure(kernobs.StageName(voiceoverStage), func(c context.Context) bool {
		return e.r.runVoiceoverPhase(c, e.runID, e.req, e.routing, e.exec, e.resumeIdx, e.result)
	}) {
		// TTS failed: the deferred cancelPrepare stops the prepare branch.
		return false
	}

	// Join the prepare branch; an error fails the run (fail-closed).
	var outcome vidRushPrepareOutcome
	kernobs.MeasureStage(e.ctx, "prepare_join", func(stageCtx context.Context) error {
		outcome = <-prepareDone
		return outcome.err
	})
	if outcome.err != nil {
		return e.fail(StageGeneratingSceneText, outcome.err)
	}
	applyVidRushPrepareProjections(e.result, outcome.result)
	e.skeletons = outcome.skeletons
	// Store the prefetched audio assets so the audio-compile phase consumes
	// them without blocking on I/O.
	e.result.AudioPrefetch = outcome.prefetched
	e.checkpoint()
	return true
}

// audioCompile runs the audio-compile + final-audio publish phases.
func (e *executionRun) audioCompile() bool {
	return e.measure(kernobs.StageName(audioCompileStage), func(c context.Context) bool {
		if !e.r.runAudioCompilePhase(c, e.runID, e.req, e.exec, e.resumeIdx, e.result) {
			return false
		}
		return e.r.publishFinalAudio(c, e.runID, e.req, e.routing, e.exec, e.result)
	})
}

// persist assembles/publishes the final video (when required) and persists
// the canonical script. It checkpoints before persisting.
func (e *executionRun) persist() bool {
	return e.measure(kernobs.StageName(stagePersistence), func(c context.Context) bool {
		if e.result.FinalVideoRequired {
			if err := e.r.assembleFinalVideo(c, e.runID, e.result); err != nil {
				return e.fail(StagePublishingDocuments, err)
			}
		}
		if e.result.FinalVideoRequired && e.r.finalVideoPublisher != nil {
			published, err := e.r.finalVideoPublisher.PublishFinalVideo(c, e.runID, *e.result.FinalVideo, e.req.Render.DriveFolderID)
			if err != nil {
				return e.fail(StagePublishingDocuments, fmt.Errorf("FINAL_VIDEO_UPLOAD_FAILED: %w", err))
			}
			e.result.FinalVideo.AssetID = published.AssetID
			e.result.FinalVideo.DriveLink = published.DriveLink
		} else if e.result.FinalVideoRequired {
			return e.fail(StagePublishingDocuments, fmt.Errorf("FINAL_VIDEO_UPLOAD_FAILED: final video publisher is not wired"))
		}
		e.checkpoint()
		return e.r.persistScript(c, e.runID, e.req, e.exec, e.resumeIdx, e.result)
	})
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
	e.r.completeRun(e.ctx, e.runID, e.result)
}
