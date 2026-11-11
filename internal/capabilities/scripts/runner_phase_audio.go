package scriptgeneration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"
	scriptaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/audio"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// audioCompileState carries the state the audio compile phase must hand to the
// phases that follow it (the overlay render and the audio finalize). The compile
// phase ends before the render because the render is measured as its own stage:
// the kernel attributes a nested stage to its enclosing stage, so a render
// sequenced inside this phase would be charged to audio_compile again.
type audioCompileState struct {
	// Step is the AUDIO_COMPILE execution step started by the compile phase. It
	// stays open across the render and the finalize so a render failure is
	// still reported against the step that owns the work.
	Step ExecutionStep
	// AudioSkipped reports that the compile phase had no audio work to do for
	// this attempt (resumed past the stage, or no timeline requested).
	AudioSkipped bool
	// OnPlanReady starts an independent render against an immutable snapshot.
	// The caller joins it before finalization; nil preserves serial callers.
	// non-nil is also what marks the overlap path: NULL callers render through
	// the serial overlay render phase instead.
	OnPlanReady func(*GenerateResult) bool
	// Barrier is the P2 overlay-language submit-barrier (running_on the detached
	// P1 plan-branch outcome while this master compile runs). The OnPlanReady
	// render fork applies its certified outcomes onto its own snapshot before
	// rendering, so a barrier-composed language is never re-submitted. nil keeps
	// serial behavior (final jobs, serial render callers, gate off).
	Barrier *overlayLanguageBarrier
}

// runAudioCompilePhase compiles the canonical timeline and the semantic
// OverlayPlan. It deliberately stops before the overlay render and before the
// editing-timeline projection: both are separate measured boundaries owned by
// runOverlayRenderPhase and runAudioFinalizePhase.
func (r *Runner) runAudioCompilePhase(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, resumeIdx int, result *GenerateResult, out *audioCompileState) bool {
	// ── Compile Audio (before document publication) ───────────────
	payloadStep, startErr := r.startExecutionStep(ctx, exec, "AUDIO_COMPILE", "audio")
	if startErr != nil {
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, startErr)
		return false
	}
	if out != nil {
		out.Step = payloadStep
	}
	var err error
	var canonicalTimeline capabilityaudio.CanonicalTimeline
	var compiledAudioPlan capabilityaudio.CompiledAudioPlan
	overlayPrepared := false
	// planBranch is the P1 detached overlay-plan computation. It is started
	// only on the COMBINED_TIMELINE path (where the master compile is heavy)
	// and stays nil everywhere else, so the serial projection fallback
	// below is unchanged for those modes.
	var planBranch *overlayPlanBranch
	// Audio mode is an explicit request-level choice. The presence of
	// generated scenes (or voiceover assets) is never a mode selector.
	mode, modeErr := capabilityaudio.ResolveAudioMode(req.Audio, false)
	if modeErr != nil {
		// Envelope validation rejects invalid audio-mode combinations
		// earlier; fail closed here for direct-runner callers.
		cause := fmt.Errorf("audio compile phase: resolve audio mode: %w", modeErr)
		return r.failAudioCompileStep(ctx, runID, exec, payloadStep, cause)
	}
	result.AudioMode = mode
	// The audio phase builds the canonical timeline whenever a timeline is
	// requested (GenerateTimeline) or the combined audio mode requires the
	// canonical audio timeline (COMBINED_TIMELINE). PipelineGen is audio-only:
	// the run stops at the certified final_audio.m4a and never requires local
	// media for a video render.
	needsTimeline := req.GenerateTimeline || mode == capabilityaudio.AudioModeCombinedTimeline
	audioSkipped := stageSkipped(resumeIdx, StageCompilingAudio) || !needsTimeline
	if !audioSkipped {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageCompilingAudio); err != nil {
			r.failExecutionStepBestEffort(ctx, exec, payloadStep, err)
			r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
			return false
		}
		if !r.validateAudioCompileSceneIdentity(ctx, runID, req, exec, payloadStep, result) {
			return false
		}
		// Stock scenes are handed to the remote Master as whole files only.
		// Round each stock scene up to the binding's declared file duration
		// BEFORE the audio plan is compiled: the certified TTS keeps its own
		// source duration and the renderer fills the tail with silence. This
		// mutates the durable scenes on purpose so the canonical timeline,
		// the overlay projections and the final job all observe one window.
		if req.FinalJob {
			alignFinalJobStockSceneDurations(result)
		}
		// ── AUDIO PHASE ─────────────────────────────────────────────
		// PipelineGen is audio-only: it compiles the canonical timeline +
		// audio plan and certifies one final_audio.m4a whenever
		// Audio == COMBINED_TIMELINE. There is no video render phase.
		if mode != capabilityaudio.AudioModeCombinedTimeline && result.FinalAudio != nil {
			cause := fmt.Errorf("%s must not carry final audio", mode)
			r.failExecutionStepBestEffort(ctx, exec, payloadStep, cause)
			r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
			return false
		}
		switch mode {
		case capabilityaudio.AudioModeCombinedTimeline:
			audioStep, startErr := r.startExecutionStep(ctx, exec, "AUDIO_COMPILE", "audio")
			if startErr != nil {
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, startErr)
				return false
			}
			if r.combinedAudioRenderer == nil {
				cause := fmt.Errorf("COMBINED_TIMELINE requires a CombinedAudioRenderer")
				r.failExecutionStepBestEffort(ctx, exec, audioStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
			// ── P1: certified-timing + overlay-plan branch ───────────
			// Every plan fact is final at audio-phase entry (per-unit word
			// timings, per-unit durations, SceneTextReady annotations), so
			// the phrase/entity projections and the sealed overlay plan
			// compile on a detached snapshot WHILE the clip-audio prepare
			// and the master composition below run. The join applies the
			// projection on this goroutine, which stays the durable result's
			// single writer (runner_overlay_plan_branch.go).
			planBranch = r.startOverlayPlanBranch(ctx, runID, req, exec, result, mode)
			// P2: the overlay-language submit-barrier renders every pending plan
			// off the detached branch outcome while the master composition below
			// runs. The OnPlanReady render fork merges it onto its own snapshot
			// before capturing its pending set (see runner_phase_overlay_plan.go).
			if out != nil && out.OnPlanReady != nil {
				out.Barrier = r.startOverlayRenderBarrier(ctx, runID, req, exec, resumeIdx, planBranch)
			}
			var audioAssets capabilityaudio.ResolvedAudioAssets
			var compileTimings AudioCompileTimings
			policy, clipAudioSource := r.combinedAudioInputs(result, req)
			// Clip audio is materialized in EVERY lane since the 2026-09-30
			// final-job fix: the remote master mixes the restored clip track, so
			// the compiled plan must carry verified local clip audio paths. The
			// old skip left AudioClip intents unresolvable here; the compile then
			// failed (or the projection dropped the track entirely).
			var clipPrepareMS int64
			clipPrepareMS, prepareErr := prepareClipAudioAssets(ctx, result, clipAudioSource, policy)
			if prepareErr != nil {
				cause := fmt.Errorf("prepare original clip audio failed: %w", prepareErr)
				r.failExecutionStepBestEffort(ctx, exec, audioStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
			compileTimings.ClipAudioPrepareMS = clipPrepareMS
			// The audio intent block (BGM/SFX) is layered onto the same
			// VO-governed timeline: asset resolution → BGM windows → loop
			// expansion → SFX placement → automation, all compiled into the
			// sealed plan by CompileAudioWithIntents. Absent intents keep the
			// legacy primary-only CompileWithMixPolicy path.
			if len(req.BackgroundMusic) > 0 || len(req.SoundEffects) > 0 || req.RandomSFXOnClipStart {
				audioSource := r.audioAssetSource
				// P1.1: use prefetched BGM/SFX paths (already resolved during TTS).
				if result.AudioPrefetch != nil && result.AudioPrefetch.AudioSource != nil {
					audioSource = result.AudioPrefetch.AudioSource
				}
				if audioSource == nil {
					cause := fmt.Errorf("audio intent block requires an audio asset resolver")
					r.failExecutionStepBestEffort(ctx, exec, audioStep, cause)
					r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
					return false
				}
				audioInput := audioCompileInput(result, req)
				canonicalTimeline, compiledAudioPlan, audioAssets, compileTimings, err = CompileCanonicalAudioPlanAudioOnlyWithIntents(ctx, audioInput, req.SourceLanguage, capabilityaudio.DefaultAudioProfile(), audioSource, policy, req.BackgroundMusic, req.SoundEffects, req.RandomSFXOnClipStart)
			} else {
				audioInput := audioCompileInput(result, req)
				canonicalTimeline, compiledAudioPlan, audioAssets, compileTimings, err = CompileCanonicalAudioPlanAudioOnly(audioInput, req.SourceLanguage, capabilityaudio.DefaultAudioProfile())
			}
			if err != nil {
				cause := fmt.Errorf("compile canonical audio plan failed: %w", err)
				r.failExecutionStepBestEffort(ctx, exec, audioStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
			if req.FinalJob && !r.alignFinalJobAudioMaster(ctx, runID, exec, audioStep, canonicalTimeline, &compiledAudioPlan) {
				return false
			}
			// The compile returns its own subtimings; the clip-audio preparation
			// measured above is the owner of ClipAudioPrepareMS, so it is
			// re-applied instead of being silently replaced by the compile's
			// probe window.
			compileTimings = mergeAudioCompileTimings(compileTimings, clipPrepareMS)
			r.recordAudioCompileOperations(ctx, compileTimings)
			if result.ResolvedScenes, err = ResolveScenes(result.Scenes, req.SourceLanguage, mode, false); err != nil {
				cause := fmt.Errorf("resolve scenes for persistence failed: %w", err)
				r.failExecutionStepBestEffort(ctx, exec, audioStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
			if out != nil && out.OnPlanReady != nil {
				result.CanonicalTimeline = &canonicalTimeline
				result.AudioPlan = &compiledAudioPlan
				var planProjected bool
				if planBranch != nil {
					planProjected = r.joinOverlayPlanProjection(ctx, runID, req, exec, payloadStep, result, planBranch)
				} else {
					planProjected = r.compileAudioOverlayPlan(ctx, runID, req, exec, payloadStep, result)
				}
				if !planProjected || !out.OnPlanReady(result) {
					return false
				}
				overlayPrepared = true
			}
			if !r.renderAndCertifyFinalAudio(ctx, runID, req, exec, audioStep, result, canonicalTimeline, compiledAudioPlan, audioAssets, compileTimings) {
				return false
			}
			// The joined finalize boundary checkpoints both certified siblings.
		case capabilityaudio.AudioModeChunkedVoiceover, capabilityaudio.AudioModeNone:
			if !r.compileTimelineOnlyAudio(ctx, runID, req, exec, payloadStep, result, mode, &canonicalTimeline) {
				return false
			}
		}
		if !r.ensureResolvedScenesForAudioCompile(ctx, runID, req, exec, payloadStep, result, mode) {
			return false
		}
		result.CanonicalTimeline = &canonicalTimeline
		if !r.projectAudioOverlayPlan(ctx, runID, req, exec, payloadStep, result, planBranch, overlayPrepared) {
			return false
		}
		r.logLocationOverlayPlan(req, result)
	}
	if out != nil {
		out.Step = payloadStep
		out.AudioSkipped = audioSkipped
	}
	return true
}

func (r *Runner) renderAndCertifyFinalAudio(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, audioStep ExecutionStep, result *GenerateResult, canonicalTimeline capabilityaudio.CanonicalTimeline, compiledAudioPlan capabilityaudio.CompiledAudioPlan, audioAssets capabilityaudio.ResolvedAudioAssets, compileTimings AudioCompileTimings) bool {
	var finalAudio FinalAudioReference
	var metrics AudioPipelineMetrics
	resumeAudio := result.FinalAudio != nil && ValidateFinalAudioReference(*result.FinalAudio, compiledAudioPlan) == nil
	if resumeAudio && r.checkpoints != nil {
		// Durable checkpoint gate on the idempotency boundary: the
		// restored reference validates in memory, but reuse is allowed
		// only when the durable checkpoint also certifies the unit's
		// completion (input fingerprint + artifact existence + artifact
		// SHA256 + processor version). A stale or unverifiable
		// completion re-renders — never an unverified reuse.
		decision, reason, decideErr := r.checkpoints.Decide(ctx, exec.JobID, capcheckpoint.StageAudio, capcheckpoint.UnitGlobal, capcheckpoint.ExpectedInput{
			InputFingerprint: compiledAudioPlan.PlanSHA256,
			ProcessorVersion: capabilityaudio.AudioContractVersion,
		})
		if decideErr != nil {
			r.log.Warn("checkpoint decide failed; re-rendering audio",
				zap.String("run_id", runID),
				zap.Error(decideErr))
			resumeAudio = false
		} else if decision == capcheckpoint.DecisionExecute {
			r.log.Info("audio checkpoint invalidated; re-rendering",
				zap.String("run_id", runID),
				zap.String("reason", reason))
			resumeAudio = false
		}
	}
	if resumeAudio {
		// A checkpointed certified artifact is the idempotency boundary.
		// Do not invoke TTS/mix/encode again on a retry.
		finalAudio = *result.FinalAudio
		if result.AudioMetrics != nil {
			metrics = *result.AudioMetrics
		}
	} else {
		// Cross-run reuse: the compiled plan hash is the work identity, so a
		// retry or a revision that leaves the plan untouched must not pay
		// mix + AAC again. The cache re-hashes the bytes before answering,
		// and the certification below still runs on the reused reference.
		if req.FinalJob {
			cacheStarted := time.Now()
			if cachedRef, ok := r.finalAudioCache.Take(compiledAudioPlan, os.TempDir()); ok {
				cached := finalAudioReferenceFromCache(cachedRef)
				// A cache hit is only accepted when it satisfies the SAME
				// contract the renderer must satisfy: a stale or foreign entry
				// costs one render, never a failed run.
				if certErr := ValidateFinalAudioReference(cached, compiledAudioPlan); certErr != nil {
					r.log.Warn("final audio cache entry does not satisfy the canonical contract; re-rendering",
						zap.String("run_id", runID),
						zap.String("plan_sha256", compiledAudioPlan.PlanSHA256),
						zap.Error(certErr))
				} else {
					finalAudio = cached
					if result.AudioMetrics != nil {
						metrics = *result.AudioMetrics
					}
					cacheMS := time.Since(cacheStarted).Milliseconds()
					metrics.CacheOutcome = scriptaudio.CacheHitPlan
					metrics.TotalMS = cacheMS
					metrics.AudioDurationMS = finalAudio.DurationMS
					r.log.Info("final audio reused from the plan cache",
						zap.String("run_id", runID),
						zap.String("plan_sha256", compiledAudioPlan.PlanSHA256),
						zap.Int64("lookup_ms", cacheMS))
				}
			}
		}
		if strings.TrimSpace(finalAudio.AssetID) == "" {
			// rust.audio_render is the external Rust render boundary
			// (pipelinegen-muscles). The canonical Run clock records the
			// whole Render invocation as an OperationReport under
			// audio_compile; mix/aac_encode/probe/hash remain the
			// owner-measured subtimings inside it.
			if opErr := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
				Stage:     kernobs.StageName(audioCompileStage),
				Component: kernobs.ComponentName("rust"),
				Operation: kernobs.OperationName("audio_render"),
			}, func(opCtx context.Context) error {
				var renderErr error
				finalAudio, metrics, renderErr = r.combinedAudioRenderer.Render(opCtx, compiledAudioPlan, audioAssets)
				return renderErr
			}); opErr != nil {
				cause := fmt.Errorf("combined audio render failed: %w", opErr)
				r.failExecutionStepBestEffort(ctx, exec, audioStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
			r.recordAudioRenderOperations(ctx, metrics)
			// Render correlation: the produced final_audio asset is
			// traceable to the render operation via its asset_id.
			if err := r.recordArtifactOperation(ctx, exec, ArtifactOperation{
				OperationID: artifactOperationID(exec.Attempt, OperationRender, "final_audio"),
				Kind:        OperationRender,
				Language:    req.SourceLanguage,
				AssetID:     finalAudio.AssetID,
				Status:      "COMPLETED",
			}); err != nil {
				r.failExecutionStepBestEffort(ctx, exec, audioStep, err)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
				return false
			}
			if req.FinalJob {
				r.finalAudioCache.Put(compiledAudioPlan, finalAudioCacheReference(finalAudio))
			}
		}
		metrics.TimelineCompileMS = compileTimings.TimelineCompileMS
		metrics.AudioPlanCompileMS = compileTimings.AudioPlanCompileMS
		metrics.ClipAudioPrepareMS = compileTimings.ClipAudioPrepareMS
		// Reused or rendered, the reference is certified by the same gate.
		if err := ValidateFinalAudioReference(finalAudio, compiledAudioPlan); err != nil {
			cause := fmt.Errorf("final audio certification failed: %w", err)
			r.failExecutionStepBestEffort(ctx, exec, audioStep, cause)
			r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
			return false
		}
	}
	// Cert-time invariant: the recorded VO source_duration_us must match
	// the certified probe durations (modulo the scene-window clamp).
	// Runs for both freshly rendered and checkpointed final audio.
	if err := ValidateVoiceoverSourceDurations(*result, req.SourceLanguage, canonicalTimeline, compiledAudioPlan); err != nil {
		cause := fmt.Errorf("voiceover source-duration certification failed: %w", err)
		r.failExecutionStepBestEffort(ctx, exec, audioStep, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	// Master invariants: scene contiguity, plan/timeline duration
	// agreement, SUM(voiceover) == CanonicalTimeline (narration-driven)
	// and final_audio within the encoder-padding tolerance. Automatic
	// for both freshly rendered and checkpointed final audio.
	if err := ValidateMasterAudioInvariants(canonicalTimeline, compiledAudioPlan, finalAudio); err != nil {
		cause := fmt.Errorf("master audio certification failed: %w", err)
		r.failExecutionStepBestEffort(ctx, exec, audioStep, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	// Validation correlation: the certified master is traceable to its
	// validation operation via the same asset_id.
	if err := r.recordArtifactOperation(ctx, exec, ArtifactOperation{
		OperationID: artifactOperationID(exec.Attempt, OperationValidation, "master"),
		Kind:        OperationValidation,
		Language:    req.SourceLanguage,
		AssetID:     finalAudio.AssetID,
		Status:      "COMPLETED",
	}); err != nil {
		r.failExecutionStepBestEffort(ctx, exec, audioStep, err)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
		return false
	}
	if r.checkpoints != nil {
		// Durable checkpoint AFTER the unit's work is certified: the
		// completion record is the resume authority (crash → restart →
		// SKIP). A write failure is logged, never a run failure: the
		// consequence is only a re-render on crash, never incorrect
		// behavior.
		if err := r.checkpoints.Complete(ctx, capcheckpoint.Checkpoint{
			JobID:            exec.JobID,
			Stage:            capcheckpoint.StageAudio,
			UnitID:           capcheckpoint.UnitGlobal,
			InputFingerprint: compiledAudioPlan.PlanSHA256,
			Status:           capcheckpoint.StatusCompleted,
			ArtifactSHA256:   finalAudio.FinalAudioSHA256,
			ArtifactURI:      finalAudio.DriveLink,
			ProcessorVersion: capabilityaudio.AudioContractVersion,
			CompletedAt:      time.Now().UTC(),
		}); err != nil {
			r.log.Warn("durable audio checkpoint write failed",
				zap.String("run_id", runID),
				zap.Error(err))
		}
	}
	if result.AudioMetrics != nil {
		metrics.TTSMS += result.AudioMetrics.TTSMS
		metrics.TTSCalls += result.AudioMetrics.TTSCalls
		metrics.TTSScenes = append(metrics.TTSScenes, result.AudioMetrics.TTSScenes...)
	}
	// Project the pipeline total from the canonical audio_render
	// operation wall (owner-measured inside the Rust render boundary)
	// — never a second time.Since timer. On the resume path the render
	// is skipped, so the checkpointed value carried over from
	// result.AudioMetrics is preserved.
	if run := kernobs.FromContext(ctx); run != nil {
		if wall := kernobs.SummarizeOperations(run.Report(), audioCompileStage, "audio_render").TotalMs; wall > 0 {
			metrics.TotalMS = wall
		}
	}
	if metrics.AudioDurationMS > 0 && metrics.TotalMS > 0 {
		metrics.AudioRTF = float64(metrics.TotalMS) / float64(metrics.AudioDurationMS)
		metrics.AudioSpeed = 1 / metrics.AudioRTF
	}
	if err := r.attachOutputAsset(ctx, exec, audioStep.StepID, finalAudio.AssetID, 0); err != nil {
		r.failExecutionStepBestEffort(ctx, exec, audioStep, err)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
		return false
	}
	if err := r.recordExecutionMetric(ctx, exec, audioStep.StepID, "audio_duration_ms", float64(finalAudio.DurationMS), "ms"); err != nil {
		r.failExecutionStepBestEffort(ctx, exec, audioStep, err)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
		return false
	}
	if err := r.completeExecutionStep(ctx, exec, audioStep); err != nil {
		r.failExecutionStepBestEffort(ctx, exec, audioStep, err)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
		return false
	}
	result.FinalAudio = &finalAudio
	result.AudioStrategy = capabilityaudio.FinalAudioCopy
	result.AudioMetrics = &metrics
	result.CanonicalTimeline = &canonicalTimeline
	result.AudioPlan = &compiledAudioPlan
	return true
}

// combinedAudioInputs picks the mix policy and clip-audio source for the
// combined master. Prefetched clip paths win over the live adapter so the
// compile never blocks on materialization that TTS already performed.
func (r *Runner) combinedAudioInputs(result *GenerateResult, req GenerateRequest) (capabilityaudio.AudioMixPolicy, ClipAudioAssetSource) {
	policy := req.MixPolicy
	if policy == "" {
		policy = capabilityaudio.MixVoiceoverWithDuckedClip
	}
	if result.AudioPrefetch != nil && result.AudioPrefetch.ClipAudioSource != nil {
		return policy, result.AudioPrefetch.ClipAudioSource
	}
	candidate, _ := r.audioAssetSource.(ClipAudioAssetSource)
	return policy, candidate
}

// compileTimelineOnlyAudio handles the modes that need a canonical timeline
// but no master render. Chunked voiceover additionally validates that every
// requested language has complete chunks before persistence.
func (r *Runner) compileTimelineOnlyAudio(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, step ExecutionStep, result *GenerateResult, mode capabilityaudio.AudioMode, timeline *capabilityaudio.CanonicalTimeline) bool {
	compiled, err := CompileCanonicalTimeline(*result)
	if err != nil {
		cause := fmt.Errorf("compile canonical timeline failed: %w", err)
		r.failExecutionStepBestEffort(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	if mode == capabilityaudio.AudioModeChunkedVoiceover {
		if err := ValidateChunkedVoiceoversForLanguages(*result, req.VoiceoverLanguages); err != nil {
			r.failExecutionStepBestEffort(ctx, exec, step, err)
			r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
			return false
		}
		result.AudioStrategy = capabilityaudio.TimelineMix
	}
	*timeline = compiled
	return true
}

func audioCompileInput(result *GenerateResult, req GenerateRequest) GenerateResult {
	if req.FinalJob {
		return finalJobAudioInput(*result, req.SourceLanguage)
	}
	return *result
}

// validateAudioCompileSceneIdentity keeps the report-only scene↔clip gate out
// of the compile flow while preserving the fail-closed opt-in.
func (r *Runner) validateAudioCompileSceneIdentity(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, step ExecutionStep, result *GenerateResult) bool {
	mismatches := AuditSceneClipIdentity(*result)
	if len(mismatches) == 0 {
		return true
	}
	if err := r.recordExecutionMetric(ctx, exec, step.StepID, "clip_identity_mismatches", float64(len(mismatches)), "count"); err != nil {
		cause := fmt.Errorf("record clip identity mismatch metric: %w", err)
		r.failExecutionStepBestEffort(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	r.log.Warn("scene↔clip identity mismatch",
		zap.Int("mismatch_count", len(mismatches)),
		zap.Strings("scene_ids", clipIdentityMismatchSceneIDs(mismatches)),
		zap.Bool("enforced", req.EnforceClipIdentity),
	)
	if !req.EnforceClipIdentity {
		return true
	}
	cause := fmt.Errorf("scene↔clip identity certification failed: %w", ValidateSceneClipIdentity(*result))
	r.failExecutionStepBestEffort(ctx, exec, step, cause)
	r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
	return false
}

// alignFinalJobAudioMaster rounds the narration length to the 24 fps video
// grid. The renderer appends silence to the master, so every spoken sample is
// preserved while the AAC stream matches the copy-only video packet boundary.
func (r *Runner) alignFinalJobAudioMaster(ctx context.Context, runID string, exec ExecutionContext, step ExecutionStep, timeline capabilityaudio.CanonicalTimeline, plan *capabilityaudio.CompiledAudioPlan) bool {
	frameResolver, err := capabilityaudio.NewFrameResolver(capabilityaudio.IntegerFrameRate(24))
	if err == nil {
		plan.MasterDurationUS, err = frameResolver.FrameAlignedDurationUS(timeline.DurationUS)
	}
	if err == nil {
		err = plan.Seal()
	}
	if err != nil {
		cause := fmt.Errorf("align final-job audio master to video frames: %w", err)
		r.failExecutionStepBestEffort(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	return true
}

func (r *Runner) ensureResolvedScenesForAudioCompile(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, step ExecutionStep, result *GenerateResult, mode capabilityaudio.AudioMode) bool {
	if len(result.ResolvedScenes) != 0 {
		return true
	}
	scenes := result.Scenes
	if req.FinalJob {
		scenes = finalJobAudioInput(*result, req.SourceLanguage).Scenes
	}
	resolved, err := ResolveScenes(scenes, req.SourceLanguage, mode, false)
	if err != nil {
		cause := fmt.Errorf("resolve scenes for persistence failed: %w", err)
		r.failExecutionStepBestEffort(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	result.ResolvedScenes = resolved
	return true
}

func (r *Runner) projectAudioOverlayPlan(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, step ExecutionStep, result *GenerateResult, branch *overlayPlanBranch, alreadyPrepared bool) bool {
	if alreadyPrepared {
		return true
	}
	if branch != nil {
		return r.joinOverlayPlanProjection(ctx, runID, req, exec, step, result, branch)
	}
	return r.compileAudioOverlayPlan(ctx, runID, req, exec, step, result)
}

func resultWithoutClipAudioIntents(result GenerateResult) GenerateResult {
	result.ResolvedScenes = nil
	result.Scenes = append([]Scene(nil), result.Scenes...)
	for i := range result.Scenes {
		scene := &result.Scenes[i]
		intents := make([]capabilityaudio.AudioIntent, 0, len(scene.AudioIntents))
		for _, intent := range scene.AudioIntents {
			if intent.Mode != capabilityaudio.AudioClip || intent.ProtectedOriginalAudio || intent.StockOriginalAudio {
				intents = append(intents, intent)
			}
		}
		scene.AudioIntents = intents
		if scene.Audio.Mode == capabilityaudio.AudioClip && !scene.Audio.ProtectedOriginalAudio && !scene.Audio.StockOriginalAudio {
			scene.Audio.Mode = capabilityaudio.AudioVoiceover
		}
	}
	return result
}
