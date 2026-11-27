// Package scriptgeneration — runner_execution.go owns the run-scoped
// execution wrapper and the explicit phase decomposition of the durable
// workflow. The public entry point (ExecuteWithContext) stays intentionally
// small; the ordering and every fail/checkpoint/return path live here as
// named business phases so each phase has exactly one semantic reason to be
// complex. All cross-cutting concerns that used to be repeated inline —
// phase timing/metrics, checkpointing, error classification/termination,
// logging — are owned ONCE by the wrapper instead of by every phase.
package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/finaljob"
	runport "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports/run"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/runstate"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/timeline"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// executionRun bundles all mutable run-scoped workflow state behind one
// wrapper so phase methods share a single source of truth and the common
// concern wrappers (measure, checkpoint, fail, log) are declared exactly
// once. It deliberately carries values — not a generic engine — so each
// phase stays an explicit named step in runExecution().
type Execution struct {
	host  Host
	ctx   context.Context
	runID string
	req   contract.GenerateRequest
	exec  scriptpkg.ExecutionContext

	// routing is the canonical artifact routing context resolved ONCE at
	// run start (godlike/06 SSOT). Downstream phases consume it and never
	// re-derive Project/Language/folder routing.
	routing scriptpkg.ArtifactRoutingContext

	// run/resumeIdx capture the durable resume-from-checkpoint state.
	run       *runstate.GenerationRun
	resumeIdx int

	// coordinator is this run's VidRush coordinator wiring (nil when the
	// run has no VidRush pipeline). It is registered in beginVidRush and
	// released via defer once the run's phases completed or failed.
	coordinator any

	// result is the durable GenerateResult assembled across phases.
	result *contract.GenerateResult

	// snapshot is the read-only scene-text projection taken before the
	// concurrent TTS and VidRush-prepare branches start.
	snapshot []SceneTextSnapshot
}

// measure runs one named business phase on the canonical Run clock, keeping
// the phases' bool contract. It is the single owner of phase timing + metrics.
func (e *Execution) measure(stage kernobs.StageName, fn func(context.Context) bool) bool {
	return e.host.MeasurePhase(e.ctx, stage, fn)
}

// checkpoint persists the current durable partial result (best-effort). It is
// the single owner of checkpoint boundaries; phases never save partial state
// ad hoc.
func (e *Execution) checkpoint() {
	e.host.Checkpoint(e.ctx, e.runID, e.result)
}

// fail classifies a terminal error and stops the run. It is the single owner
// of error classification (error_code, failed_stage, attempt_count,
// next_retry_at) via failRunWithRetry, and always returns false so a phase
// can `return e.fail(...)`.
func (e *Execution) fail(stage runstate.Stage, err error) bool {
	e.host.FailRunWithRetry(e.ctx, e.runID, stage, err)
	return false
}

// log is the single logging seam for phases (nil-logger safe).
func (e *Execution) log(msg string, fields ...zap.Field) {
	if e.host.Logger() != nil {
		e.host.Logger().Info(msg, append([]zap.Field{zap.String("run_id", e.runID)}, fields...)...)
	}
}

// start resolves the canonical routing context, loads resume state from the
// repository, sets the run RUNNING for new runs, and derives the resume
// index + attempt. It returns false (already completed / terminal error)
// when the workflow must not continue.
func (e *Execution) start() bool {
	e.log("scriptgeneration: starting execution",
		zap.String("source_type", string(e.req.Source.Type)),
	)

	// Resolve the canonical artifact routing context once before any I/O.
	routing, resolveErr := e.req.ResolveArtifactRoutingContext()
	if resolveErr != nil {
		return e.fail(runstate.StagePublishingDocuments, resolveErr)
	}
	e.routing = routing

	// Determine resume stage from the existing run (if any). Only a typed
	// "not found" starts a fresh run: any other read failure (timeout, closed
	// connection, decode error) must fail the attempt instead of overwriting the
	// checkpoint with a from-scratch run, which would re-pay work the durable
	// result already certified.
	run, err := e.host.GetRun(e.ctx, e.runID)
	e.resumeIdx = -1 // -1 means start from beginning
	if err != nil && !errors.Is(err, runport.ErrRunNotFound) {
		return e.fail(runstate.StageNormalizing, fmt.Errorf("load run %s: %w", e.runID, err))
	}
	if err == nil && run != nil {
		resumeStage := runstate.ResumeFrom(run)
		if run.Status == runstate.RunStatusFailed && run.FailedStage == runstate.StagePublishingDocuments && missingUnpublishedFinalAudio(run.Result) {
			// A publish-stage retry normally resumes at publication. That cannot
			// recover a final-audio file which disappeared from local storage:
			// the same stale path would be checked again. Re-enter audio compile,
			// discard only the lost local artifact reference, and keep the already
			// rendered overlay references in the durable result.
			resumeStage = runstate.StageCompilingAudio
			run.Result.FinalAudio = nil
			e.host.Logger().Warn("final audio artifact is missing; retrying from audio compile",
				zap.String("run_id", e.runID),
			)
		}
		// Adopt the durable result from the checkpoint BEFORE deciding the resume
		// stage. Repository.Get returns the checkpointed Result, and every phase
		// that runs BEFORE the resume index is skipped — so without this adoption
		// a resume from PUBLISHING_DOCUMENTS (including a resume from the
		// CORE_READY boundary) would continue against an empty result and publish
		// nothing, silently discarding the completed core work the checkpoint was
		// written to preserve. A fresh run has no checkpointed result, so this is
		// a no-op there.
		if e.result == nil && run.Result != nil {
			e.result = run.Result
		}
		// A checkpoint whose overlay render no longer binds to its sealed plan
		// (the Kaufmann failure) cannot be repaired by deleting the reference: a
		// run resumed at PUBLISHING_DOCUMENTS never re-enters the overlay phase,
		// so the handoff would fail on a missing render instead. Archive the
		// incompatible reference and move the resume point back to the stage
		// that owns the render.
		var repaired bool
		resumeStage, repaired = e.host.RepairOverlayCheckpointOnResume(e.ctx, e.runID, resumeStage, e.result)
		if repaired {
			// The repair moved the durable stage back to COMPILING_AUDIO. Keep
			// the in-memory aggregate consistent too: phases inspect it and the
			// attempt must no longer look like the FAILED publish attempt.
			run.Status = runstate.RunStatusRunning
			run.CurrentStage = resumeStage
			run.FailedStage = ""
		}
		if resumeStage == runstate.StageCompleted {
			e.host.Logger().Info("run already completed", zap.String("run_id", e.runID))
			return false
		}
		e.resumeIdx = runstate.StageIndex(resumeStage)
		e.host.Logger().Info("resuming from checkpoint",
			zap.String("run_id", e.runID),
			zap.String("resume_stage", string(resumeStage)),
			zap.Int("attempt", run.AttemptCount+1),
		)
		e.run = run
	} else {
		// New run (or a repository that reports a nil run without a typed error)
		// — set RUNNING.
		if err := e.host.UpdateStage(e.ctx, e.runID, runstate.RunStatusRunning, runstate.StageNormalizing); err != nil {
			return e.fail(runstate.StageNormalizing, err)
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

func missingUnpublishedFinalAudio(result *contract.GenerateResult) bool {
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
func (e *Execution) normalize() bool {
	return e.measure(kernobs.StageName(kernobs.StageRunNormalize), func(c context.Context) bool {
		return e.host.RunNormalizePhase(c, e.runID, e.exec, e.resumeIdx)
	})
}

// mediaPreflightPhase runs the P0.5 fail-fast media verification after
// normalization and before any scene-text generation. It is deliberately
// synchronous: fixed intro/outro assets, original audio, and source windows
// must be certified before the LLM, translator, or TTS can start.
func (e *Execution) mediaPreflightPhase() bool {
	return e.measure(kernobs.StageRunMediaPreflight, func(c context.Context) bool {
		if (e.req.FinalJob && needsStockWindowPreparation(e.req)) || e.req.NeedsGPUEffectCapabilityPreflight() {
			checker, ok := e.host.(interface {
				CheckFinalJobCapabilities(context.Context, contract.GenerateRequest) error
			})
			if !ok {
				if e.req.NeedsGPUEffectCapabilityPreflight() {
					return e.fail(runstate.StagePreflight, fmt.Errorf("UNSUPPORTED_GPU_EFFECT: renderer capability preflight is not wired"))
				}
				return e.fail(runstate.StagePreflight, fmt.Errorf("REMOTE_CAPABILITY_MISSING: %s capability preflight is not wired", finaljob.FinalJobStockSourceCertificationCapability))
			}
			if err := checker.CheckFinalJobCapabilities(c, e.req); err != nil {
				return e.fail(runstate.StagePreflight, err)
			}
		}
		if stageSkipped(e.resumeIdx, runstate.StagePreflight) {
			if err := e.validateEnabledVidRushProviders(c); err != nil {
				return e.fail(runstate.StagePreflight, err)
			}
			return true
		}
		if err := e.host.UpdateStage(c, e.runID, runstate.RunStatusRunning, runstate.StagePreflight); err != nil {
			return e.fail(runstate.StagePreflight, err)
		}
		if e.host.MediaPreflight() == nil {
			if err := e.validateEnabledVidRushProviders(c); err != nil {
				return e.fail(runstate.StagePreflight, err)
			}
			if e.req.Intro == nil && e.req.Outro == nil {
				return true
			}
			result := PreflightResult{Failures: []PreflightFailure{{
				Category: "fixed_media",
				Detail:   "media preflight is not wired — fixed media cannot be certified before generation",
			}}}
			return e.fail(runstate.StagePreflight, result.AsError())
		}
		if _, ok := e.host.MediaPreflight().(VidRushProviderAvailabilityPreflight); !ok {
			if err := e.validateEnabledVidRushProviders(c); err != nil {
				return e.fail(runstate.StagePreflight, err)
			}
		}
		result := e.host.MediaPreflight().Run(c, e.req)
		if err := result.AsError(); err != nil {
			e.host.Logger().Warn("media preflight FAILED — run aborted before generation",
				zap.String("run_id", e.runID),
				zap.String("failures", result.Error()))
			return e.fail(runstate.StagePreflight, err)
		}
		e.host.Logger().Info("media preflight completed",
			zap.String("run_id", e.runID),
			zap.Int64("wall_ms", result.WallMS))
		return true
	})
}

func needsStockWindowPreparation(req contract.GenerateRequest) bool {
	return finaljob.NeedsStockWindowPreparation(req)
}

func (e *Execution) validateEnabledVidRushProviders(ctx context.Context) error {
	return e.host.ValidateEnabledVidRushProviders(ctx, e.req)
}

// beginVidRushPhase registers run-scoped VidRush wiring only after the
// synchronous media preflight has passed. The coordinator lifetime remains
// owned by the caller so concurrent runs stay isolated.
func (e *Execution) beginVidRushPhase() bool {
	var beginVidRushErr error
	if err := kernobs.MeasureStage(e.ctx, kernobs.StageBeginVidRush, func(stageCtx context.Context) error {
		e.coordinator, beginVidRushErr = e.host.BeginVidRush(stageCtx, e.runID, e.req)
		return beginVidRushErr
	}); err != nil && beginVidRushErr == nil {
		beginVidRushErr = err
	}
	if beginVidRushErr != nil {
		return e.fail(runstate.StageNormalizing, beginVidRushErr)
	}
	return true
}

// generate runs scene-text generation (the Gemma phase) and records the
// generate-phase KPI milestones. It runs only after media preflight passes.
func (e *Execution) generate() bool {
	ok := e.measure(kernobs.StageGenerate, func(c context.Context) bool {
		var phaseOK bool
		e.result, phaseOK = e.host.RunSceneTextPhase(c, e.runID, e.req, e.routing, e.exec, e.run, e.resumeIdx)
		if !phaseOK || e.result == nil {
			return phaseOK
		}
		// Fail-fast pre-TTS audio gate: the source ranges are already final in
		// the generated scenes, so an impossible clip window (the r1 failure)
		// must abort here instead of after translation, VidRush/scene analysis,
		// TTS and audio compile. The canonical audio contract still validates
		// the resolved timeline later; this is the cheap early mirror.
		if err := timeline.ValidateAudioSourceRanges(e.result.Scenes); err != nil {
			e.fail(runstate.StageGeneratingVoiceovers, fmt.Errorf("audio source range preflight: %w", err))
			return false
		}
		return true
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
func (e *Execution) ensureResult() {
	if e.result == nil {
		e.result = &contract.GenerateResult{Scenes: []contract.Scene{}, Render: e.req.Render, Title: e.req.Title, OutputName: e.req.OutputName, VoiceoverGroup: e.req.ScriptParams.VoiceoverGroup}
	}
}

// translate runs the translation phase on the canonical SceneTextReady
// boundary (it depends only on the final scene text).
func (e *Execution) translate() bool {
	return e.measure(kernobs.StageName(kernobs.StageRunTranslation), func(c context.Context) bool {
		return e.host.RunTranslationPhase(c, e.runID, e.req, e.exec, e.resumeIdx, e.result)
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
func (e *Execution) sceneTextReady() bool {
	e.snapshot = e.host.SnapshotSceneText(e.result.Scenes, e.req.SourceLanguage, e.req)

	ok := e.parallelFanOut()
	if ok {
		e.result.SourceTrace = e.host.SourceTraceFromResult(e.result)
	}
	return ok
}

// parallelFanOut runs the production SceneTextReady DAG: the VidRush join +
// overlay.prepare branch (DocsPrepare + audio prefetch) runs concurrently
// with TTS, then the branch is joined and its projections applied.
func (e *Execution) parallelFanOut() bool {
	// Freeze the fields translated NLP consumes before TTS starts mutating
	// scene structs. The semantic branch uses this detached projection while
	// voiceover workers update e.result.Scenes concurrently.
	nlpInput := e.host.LocalizedNLPSnapshot(e.result)
	prepareCtx, cancelPrepare := context.WithCancel(e.ctx)
	defer cancelPrepare()
	// Start semantic enrichment first and independently. Audio prefetch runs on
	// its own branch so it overlaps both NLP and TTS.
	branchStart := time.Now()
	semanticDone := make(chan PrepareOutcome, 1)
	go func() {
		res, err := e.host.RunVidRushJoinAndPrepare(prepareCtx, e.runID, e.req, e.snapshot)
		if err != nil {
			semanticDone <- PrepareOutcome{Err: err, CompletedAt: time.Now()}
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
		localized, nlpErr := e.host.ComputeLocalizedAnnotations(prepareCtx, e.req, nlpInput, res.Annotations)
		semanticDone <- PrepareOutcome{Result: res, LocalizedAnnotations: localized, NLPErr: nlpErr, CompletedAt: time.Now()}
	}()

	// Audio prefetch is independent of semantic enrichment; keep it on a
	// second branch so it also overlaps both NLP and TTS.
	assetsDone := make(chan PrepareOutcome, 1)
	go func() {
		// ── P1.1 Audio prefetch ──────────────────────────────
		// Resolve BGM/SFX assets and materialize original clip audio in
		// parallel with TTS. Best-effort: skip when the source is nil.
		var prefetched *contract.AudioPrefetchResult
		if e.host.AudioAssetSource() != nil && (len(e.req.BackgroundMusic) > 0 || len(e.req.SoundEffects) > 0 || len(e.req.ClipStartSoundEffects) > 0 || e.req.RandomSFXOnClipStart ||
			e.req.MixPolicy.Normalize() == capabilityaudio.MixVoiceoverWithDuckedClip) {
			bgmIDs := make([]string, len(e.req.BackgroundMusic))
			for i, b := range e.req.BackgroundMusic {
				bgmIDs[i] = b.AssetID
			}
			sfxIDs := make([]string, 0, len(e.req.SoundEffects)+len(e.req.ClipStartSoundEffects))
			for _, s := range e.req.SoundEffects {
				sfxIDs = append(sfxIDs, s.AssetID)
			}
			for _, cue := range e.req.ClipStartSoundEffects {
				sfxIDs = append(sfxIDs, cue.AssetID)
			}
			if e.req.RandomSFXOnClipStart {
				clipStartTimeline, timelineErr := scriptaudio.CompileCanonicalTimeline(*e.result)
				if timelineErr != nil {
					e.host.Logger().Warn("clip-start SFX prefetch timeline unavailable; compile will resolve assets synchronously", zap.String("run_id", e.runID), zap.Error(timelineErr))
				} else {
					excluded := make([]string, 0, len(e.req.ClipStartSoundEffects))
					for _, cue := range e.req.ClipStartSoundEffects {
						excluded = append(excluded, cue.ClipID)
					}
					for _, intent := range scriptaudio.RandomClipStartSFXIntentsWithPoolExcluding(clipStartTimeline, e.host.SoundEffectPool(prepareCtx, e.runID), excluded) {
						sfxIDs = append(sfxIDs, intent.AssetID)
					}
				}
			}
			// Final jobs mix the original clip track too (the 2026-09-30
			// final-job fix), so their clip audio belongs in the same
			// TTS-overlap prefetch as every other lane. Skipping it here only
			// moved the downloads into audio_compile after TTS had finished.
			var clipIDs []string
			for _, s := range e.snapshot {
				clipIDs = append(clipIDs, s.ClipIDs...)
			}
			var clipAudioSource contract.ClipAudioAssetSource
			if candidate, ok := e.host.AudioAssetSource().(contract.ClipAudioAssetSource); ok {
				clipAudioSource = candidate
			}
			// The prefetch is BOUNDED by its own budget: it overlaps TTS and
			// the render fan-out, but a stalled asset resolution must never
			// delay the join (and therefore the audio stage) indefinitely.
			// Whatever is cached when the budget expires is kept; the
			// remainder is resolved synchronously by audio compile through
			// the same cache adapters (their fall-through path).
			prefetchCtx, cancelPrefetch := context.WithTimeout(prepareCtx, scriptaudio.AudioPrefetchBudget)
			pf, pfErr := scriptaudio.PrefetchAudioAssets(prefetchCtx, bgmIDs, sfxIDs, e.host.AudioAssetSource(), clipIDs, clipAudioSource, e.req.MixPolicy)
			cancelPrefetch()
			switch {
			case pfErr != nil:
				e.host.Logger().Warn("audio prefetch not attempted — audio compile will run with synchronous resolution",
					zap.String("run_id", e.runID),
					zap.Error(pfErr))
			case pf.Degraded:
				// Partial success is kept: the cached assets are served from
				// memory and only the missing ones fall through to I/O.
				prefetched = pf
				e.host.Logger().Warn("audio prefetch degraded — part of the audio I/O will be resolved synchronously",
					zap.String("run_id", e.runID),
					zap.Int("requested", pf.RequestedCount()),
					zap.Int("cached", pf.CachedCount()),
					zap.Int64("duration_ms", pf.DurationMS),
					zap.Strings("failures", pf.Failures))
			default:
				prefetched = pf
				e.host.Logger().Info("audio prefetch complete",
					zap.String("run_id", e.runID),
					zap.Int("requested", pf.RequestedCount()),
					zap.Int("cached", pf.CachedCount()),
					zap.Int64("duration_ms", pf.DurationMS))
			}
		}
		assetsDone <- PrepareOutcome{
			Prefetched:  prefetched,
			CompletedAt: time.Now(),
		}
	}()

	// TTS runs in the main goroutine, in parallel with the prepare branch.
	if !e.measure(kernobs.StageName(kernobs.StageRunVoiceover), func(c context.Context) bool {
		return e.host.RunVoiceoverPhase(c, e.runID, e.req, e.routing, e.exec, e.resumeIdx, e.result)
	}) {
		// TTS failed: the deferred cancelPrepare stops the prepare branch.
		return false
	}

	// Join both early branches; an error fails the run (fail-closed). The
	// semantic branch is intentionally joined separately from the assets branch
	// so its start is never delayed by document rendering or audio prefetch.
	var semanticOutcome PrepareOutcome
	var assetsOutcome PrepareOutcome
	joinStart := time.Now()
	if err := kernobs.MeasureStage(e.ctx, "prepare_join", func(stageCtx context.Context) error {
		// The two branch waits are measured as their own operations inside the
		// stage, so prepare_join wall is no longer unattributed: the semantic
		// wait is the provider_resolver's barrier and the assets wait is the
		// audio-prefetch barrier (ELIM-02). The receives are sequential on
		// purpose: the second operation is only the ADDITIONAL wait the first
		// one did not already cover.
		semanticErr := kernobs.MeasureOperation(stageCtx, kernobs.OperationInfo{
			Stage: kernobs.StageName("prepare_join"), Component: kernobs.ComponentProviderResolver, Operation: kernobs.OperationJoinBarrier,
		}, func(context.Context) error {
			semanticOutcome = <-semanticDone
			return semanticOutcome.Err
		})
		_ = kernobs.MeasureOperation(stageCtx, kernobs.OperationInfo{
			Stage: kernobs.StageName("prepare_join"), Component: kernobs.ComponentFFmpeg, Operation: kernobs.OperationJoinBarrier,
		}, func(context.Context) error {
			assetsOutcome = <-assetsDone
			return nil
		})
		return semanticErr
	}); err != nil && semanticOutcome.Err == nil {
		semanticOutcome.Err = err
	}
	if e.result != nil {
		e.result.PrepareJoin = BuildPrepareJoinBreakdown(
			branchStart, joinStart, time.Now(),
			semanticOutcome.CompletedAt, assetsOutcome.CompletedAt)
	}
	if semanticOutcome.Err != nil {
		return e.fail(runstate.StageGeneratingSceneText, semanticOutcome.Err)
	}
	e.host.ApplyVidRushPrepareProjections(e.result, semanticOutcome.Result)
	if semanticOutcome.NLPErr != nil {
		return e.fail(runstate.StageTranslatingScenes, semanticOutcome.NLPErr)
	}
	// Apply the translated annotations on THIS goroutine: the semantic branch
	// only computed them, so the durable result has exactly one writer.
	e.host.ApplyLocalizedAnnotations(e.result, semanticOutcome.LocalizedAnnotations)
	// Store the prefetched audio assets so the audio-compile phase consumes
	// them without blocking on I/O.
	e.result.AudioPrefetch = assetsOutcome.Prefetched
	e.checkpoint()
	return true
}

func (e *Execution) finalJob() bool {
	if !e.req.FinalJob {
		return true
	}
	// The complete final video is Master-owned (PRODUCER-MASTER-BOUNDARY.md):
	// the Producer renders clips, TTS and overlays and never concatenates the
	// final master locally. Persistence may still join an overlay-only
	// deliverable for a non-final run, but a final_job always hands off.
	return e.measure(kernobs.StageRunFinalJob, func(c context.Context) bool {
		return e.host.SubmitFinalJob(c, e.runID, e.req, e.result)
	})
}

// recordPublicationDrainWait closes the interval spent joining the bounded
// publication pools. Before this, the join sat between audio_render and
// post_writer_finalize/objectstore_upload with no operation and no wait, so it
// was counted as idle even when the pool was uploading. A drain with nothing
// in flight returns immediately and is not recorded.
func recordPublicationDrainWait(ctx context.Context, started time.Time) {
	finished := time.Now()
	if finished.Sub(started) < time.Millisecond {
		return
	}
	kernobs.RecordWait(ctx, kernobs.WaitInfo{
		Kind:       kernobs.WaitOutboxDelivery,
		Component:  kernobs.ComponentDrive,
		StartedAt:  started,
		FinishedAt: finished,
	})
}

// persist stores the canonical script. The local final-video join runs here;
// a remote final render (forced or used as fallback) runs after persistence.
func (e *Execution) persist() bool {
	if !e.measure(kernobs.StageName(kernobs.StageRunPersistence), func(c context.Context) bool {
		e.checkpoint()
		return e.host.PersistScript(c, e.runID, e.req, e.exec, e.resumeIdx, e.result)
	}) {
		return false
	}
	return true
}

// complete finalizes a successful run (render-set certification, critical-path
// summary, pipeline invariants) and marks it COMPLETED.
func (e *Execution) complete() {
	// Join the async voiceover publish pool on EVERY path, including runs
	// with docs removed: the published links are consumed by the cross-run
	// voiceover cache and the media registry, and a run that completes while
	// its uploads are still in flight orphans them past its own lifecycle
	// (K2). The uploads themselves are deliberately not gated on documents.
	if e.host.VoiceoverPublishDrainer() != nil {
		publishDrainStarted := time.Now()
		e.host.VoiceoverPublishDrainer().Wait()
		recordPublicationDrainWait(e.ctx, publishDrainStarted)
		voiceoverGenerated := 0
		if e.result != nil && e.result.AudioMetrics != nil {
			voiceoverGenerated = e.result.AudioMetrics.VoiceoverGenerated
		}
		kernobs.RecordStage(e.ctx, kernobs.StageInfo{Stage: "publish_pool_drain", ItemsInput: int64(voiceoverGenerated)}, publishDrainStarted, time.Now(), nil)
	}
	// Overlay Drive publication is NOT joined here: it is a delivery concern.
	// The render is complete once every artifact has a certified object-store
	// locator, and the durable outbox owns the upload + retry that may land
	// after this run is COMPLETED. A pending or failing Drive copy therefore
	// cannot delay or fail a rendered run; /full distinguishes the two states
	// through render_status and drive_publication.
	if err := e.host.CompleteRun(e.ctx, e.runID, e.result); err != nil {
		// Completion persistence is part of the run's contract: without the
		// checkpoint and the COMPLETED transition the run is not done. Mark it
		// failed at the publication boundary (idempotent on resume) and let the
		// durable state carry the error; a log line alone would report success
		// while the row still says RUNNING.
		e.host.FailRunWithRetry(e.ctx, e.runID, runstate.StagePublishingDocuments,
			fmt.Errorf("complete run: %w", err))
	}
}
