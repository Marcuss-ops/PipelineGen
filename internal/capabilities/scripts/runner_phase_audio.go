package scriptgeneration

import (
	"context"
	"fmt"
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
	"time"
)

func (r *Runner) runAudioCompilePhase(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, resumeIdx int, result *GenerateResult) bool {
	// ── Compile Audio (before document publication) ───────────────
	payloadStep, startErr := r.startExecutionStep(ctx, exec, "AUDIO_COMPILE", "audio")
	if startErr != nil {
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, startErr)
		return false
	}
	var err error
	var canonicalTimeline capabilityaudio.CanonicalTimeline
	var compiledAudioPlan capabilityaudio.CompiledAudioPlan
	// Audio mode is an explicit request-level choice. The presence of
	// generated scenes (or voiceover assets) is never a mode selector.
	mode, modeErr := capabilityaudio.ResolveAudioMode(req.Audio, false)
	if modeErr != nil {
		// Envelope validation rejects invalid audio-mode combinations
		// earlier; fail closed here for direct-runner callers.
		cause := fmt.Errorf("audio compile phase: resolve audio mode: %w", modeErr)
		r.failExecutionStep(ctx, exec, payloadStep, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
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
			r.failExecutionStep(ctx, exec, payloadStep, err)
			r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
			return false
		}
		// Scene↔clip identity gate. Report-only by default: mismatches are
		// recorded as a metric and a warning so existing runs are not blocked
		// while the signal is validated. EnforceClipIdentity promotes it to a
		// fail-closed gate. Independent of audio/video duration logic.
		if mismatches := AuditSceneClipIdentity(*result); len(mismatches) > 0 {
			if err := r.recordExecutionMetric(ctx, exec, payloadStep.StepID, "clip_identity_mismatches", float64(len(mismatches)), "count"); err != nil {
				cause := fmt.Errorf("record clip identity mismatch metric: %w", err)
				r.failExecutionStep(ctx, exec, payloadStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
			r.log.Warn("scene↔clip identity mismatch",
				zap.Int("mismatch_count", len(mismatches)),
				zap.Strings("scene_ids", clipIdentityMismatchSceneIDs(mismatches)),
				zap.Bool("enforced", req.EnforceClipIdentity),
			)
			if req.EnforceClipIdentity {
				cause := fmt.Errorf("scene↔clip identity certification failed: %w", ValidateSceneClipIdentity(*result))
				r.failExecutionStep(ctx, exec, payloadStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
		}
		// ── AUDIO PHASE ─────────────────────────────────────────────
		// PipelineGen is audio-only: it compiles the canonical timeline +
		// audio plan and certifies one final_audio.m4a whenever
		// Audio == COMBINED_TIMELINE. There is no video render phase.
		if mode != capabilityaudio.AudioModeCombinedTimeline && result.FinalAudio != nil {
			cause := fmt.Errorf("%s must not carry final audio", mode)
			r.failExecutionStep(ctx, exec, payloadStep, cause)
			r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
			return false
		}
		if mode == capabilityaudio.AudioModeCombinedTimeline {
			audioStep, startErr := r.startExecutionStep(ctx, exec, "AUDIO_COMPILE", "audio")
			if startErr != nil {
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, startErr)
				return false
			}
			if r.combinedAudioRenderer == nil {
				cause := fmt.Errorf("COMBINED_TIMELINE requires a CombinedAudioRenderer")
				r.failExecutionStep(ctx, exec, audioStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
			started := time.Now()
			var audioAssets capabilityaudio.ResolvedAudioAssets
			var compileTimings AudioCompileTimings
			// The audio intent block (BGM/SFX) is layered onto the same
			// VO-governed timeline: asset resolution → BGM windows → loop
			// expansion → SFX placement → automation, all compiled into the
			// sealed plan by CompileAudioWithIntents. Absent intents keep the
			// legacy primary-only CompileWithMixPolicy path.
			if len(req.BackgroundMusic) > 0 || len(req.SoundEffects) > 0 {
				if r.audioAssetSource == nil {
					cause := fmt.Errorf("audio intent block requires an audio asset resolver")
					r.failExecutionStep(ctx, exec, audioStep, cause)
					r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
					return false
				}
				policy := req.MixPolicy
				if policy == "" {
					policy = capabilityaudio.MixVoiceoverWithDuckedClip
				}
				canonicalTimeline, compiledAudioPlan, audioAssets, compileTimings, err = CompileCanonicalAudioPlanAudioOnlyWithIntents(ctx, *result, req.SourceLanguage, capabilityaudio.DefaultAudioProfile(), r.audioAssetSource, policy, req.BackgroundMusic, req.SoundEffects)
			} else {
				canonicalTimeline, compiledAudioPlan, audioAssets, compileTimings, err = CompileCanonicalAudioPlanAudioOnly(*result, req.SourceLanguage, capabilityaudio.DefaultAudioProfile())
			}
			if err != nil {
				cause := fmt.Errorf("compile canonical audio plan failed: %w", err)
				r.failExecutionStep(ctx, exec, audioStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
			r.recordAudioCompileOperations(ctx, compileTimings)
			if result.ResolvedScenes, err = ResolveScenes(result.Scenes, req.SourceLanguage, mode, false); err != nil {
				cause := fmt.Errorf("resolve scenes for persistence failed: %w", err)
				r.failExecutionStep(ctx, exec, audioStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
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
					r.failExecutionStep(ctx, exec, audioStep, cause)
					r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
					return false
				}
				metrics.TimelineCompileMS = compileTimings.TimelineCompileMS
				metrics.AudioPlanCompileMS = compileTimings.AudioPlanCompileMS
				metrics.ClipAudioPrepareMS = compileTimings.ClipAudioPrepareMS
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
					r.failExecutionStep(ctx, exec, audioStep, err)
					r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
					return false
				}
				if err := ValidateFinalAudioReference(finalAudio, compiledAudioPlan); err != nil {
					cause := fmt.Errorf("final audio certification failed: %w", err)
					r.failExecutionStep(ctx, exec, audioStep, cause)
					r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
					return false
				}
			}
			// Cert-time invariant: the recorded VO source_duration_us must match
			// the certified probe durations (modulo the scene-window clamp).
			// Runs for both freshly rendered and checkpointed final audio.
			if err := ValidateVoiceoverSourceDurations(*result, req.SourceLanguage, canonicalTimeline, compiledAudioPlan); err != nil {
				cause := fmt.Errorf("voiceover source-duration certification failed: %w", err)
				r.failExecutionStep(ctx, exec, audioStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
			// Master invariants: scene contiguity, plan/timeline duration
			// agreement, SUM(voiceover) == CanonicalTimeline (narration-driven)
			// and final_audio within the encoder-padding tolerance. Automatic
			// for both freshly rendered and checkpointed final audio.
			if err := ValidateMasterAudioInvariants(canonicalTimeline, compiledAudioPlan, finalAudio); err != nil {
				cause := fmt.Errorf("master audio certification failed: %w", err)
				r.failExecutionStep(ctx, exec, audioStep, cause)
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
				r.failExecutionStep(ctx, exec, audioStep, err)
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
			metrics.TotalMS = time.Since(started).Milliseconds()
			if metrics.AudioDurationMS > 0 && metrics.TotalMS > 0 {
				metrics.AudioRTF = float64(metrics.TotalMS) / float64(metrics.AudioDurationMS)
				metrics.AudioSpeed = 1 / metrics.AudioRTF
			}
			if err := r.attachOutputAsset(ctx, exec, audioStep.StepID, finalAudio.AssetID, 0); err != nil {
				r.failExecutionStep(ctx, exec, audioStep, err)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
				return false
			}
			if err := r.recordExecutionMetric(ctx, exec, audioStep.StepID, "audio_duration_ms", float64(finalAudio.DurationMS), "ms"); err != nil {
				r.failExecutionStep(ctx, exec, audioStep, err)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
				return false
			}
			if err := r.completeExecutionStep(ctx, exec, audioStep); err != nil {
				r.failExecutionStep(ctx, exec, audioStep, err)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
				return false
			}
			result.FinalAudio = &finalAudio
			result.AudioStrategy = capabilityaudio.FinalAudioCopy
			result.AudioMetrics = &metrics
			result.CanonicalTimeline = &canonicalTimeline
			result.AudioPlan = &compiledAudioPlan
			r.checkpoint(ctx, runID, result)
		} else if mode == capabilityaudio.AudioModeChunkedVoiceover {
			canonicalTimeline, err = CompileCanonicalTimeline(*result)
			if err != nil {
				cause := fmt.Errorf("compile canonical timeline failed: %w", err)
				r.failExecutionStep(ctx, exec, payloadStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
			if err := ValidateChunkedVoiceovers(*result); err != nil {
				r.failExecutionStep(ctx, exec, payloadStep, err)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
				return false
			}
			result.AudioStrategy = capabilityaudio.TimelineMix
		} else if mode == capabilityaudio.AudioModeNone {
			canonicalTimeline, err = CompileCanonicalTimeline(*result)
			if err != nil {
				cause := fmt.Errorf("compile canonical timeline failed: %w", err)
				r.failExecutionStep(ctx, exec, payloadStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
		}
		if len(result.ResolvedScenes) == 0 {
			result.ResolvedScenes, err = ResolveScenes(result.Scenes, req.SourceLanguage, mode, false)
			if err != nil {
				cause := fmt.Errorf("resolve scenes for persistence failed: %w", err)
				r.failExecutionStep(ctx, exec, payloadStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
		}
		result.CanonicalTimeline = &canonicalTimeline
		// ── PHRASE TIMING PROJECTION ──────────────────────────────
		// Derive the phrase→timestamp projection from the per-scene voiceover
		// timing artifacts (captured in the same synthesis stream as the
		// audio). Fail-closed: a scene with timing that cannot anchor its
		// narration verbatim fails the run.
		if err := compileResultPhraseTimings(result, req.SourceLanguage); err != nil {
			cause := fmt.Errorf("phrase timing compilation failed: %w", err)
			r.failExecutionStep(ctx, exec, payloadStep, cause)
			r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
			return false
		}
		// ── ENTITY TIMELINE PROJECTION ───────────────────────────
		// Derive the entity→timestamp projection from the same canonical
		// word timing: every entity occurrence is anchored to the REAL
		// voiceover (first spoken word start → last spoken word end) and
		// mapped onto the final timeline via the scene's canonical offset.
		// Fail-closed like the phrase projection: a scene that carries both
		// annotations and word timing must speak every entity verbatim.
		if err := compileResultEntityTimeline(result, req.SourceLanguage); err != nil {
			cause := fmt.Errorf("entity timeline compilation failed: %w", err)
			r.failExecutionStep(ctx, exec, payloadStep, cause)
			r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
			return false
		}
		// ── OVERLAY PLAN PROJECTION ──────────────────────────────
		// Derive the semantic OverlayPlan from the SAME certified surfaces
		// (phrase timings, entity timeline, word timing, annotations): every
		// overlay item is anchored to real timestamps, never estimates. The
		// plan id is the run id so the RenderingGen queue job is idempotent
		// on replay. Fail-closed like the phrase/entity projections: a scene
		// that carried timing surfaces must project, or the run fails.
		if err := compileResultOverlayPlan(result, req.SourceLanguage, runID, req.Project, r.overlayCanvas); err != nil {
			cause := fmt.Errorf("overlay plan compilation failed: %w", err)
			r.failExecutionStep(ctx, exec, payloadStep, cause)
			r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
			return false
		}
		// Render only after canonical timing and the semantic OverlayPlan are
		// frozen. The prepare job, when configured, already ran independently.
		if r.overlayRenderEnqueuer != nil && result.OverlayPlan != nil {
			ref, renderErr := r.overlayRenderEnqueuer.EnqueueChrononPlan(ctx, *result.OverlayPlan)
			if renderErr != nil {
				cause := fmt.Errorf("overlay render failed: %w", renderErr)
				r.failExecutionStep(ctx, exec, payloadStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
			result.OverlayRender = &ref
		}
		// ── EDITING TIMELINE PROJECTION ──────────────────────────────
		// Build the canonical EditingTimelineV1 from frozen facts. This is
		// the single projection consumed by downstream editing; no component
		// maintains a second independently calculated timeline.
		if et, err := BuildEditingTimeline(result); err != nil {
			cause := fmt.Errorf("editing timeline compilation failed: %w", err)
			r.failExecutionStep(ctx, exec, payloadStep, cause)
			r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
			return false
		} else if et != nil {
			result.EditingTimeline = et
		}
		r.log.Info("audio compile complete",
			zap.String("run_id", runID),
			zap.String("audio_mode", string(mode)),
		)
		r.checkpoint(ctx, runID, result)
	}
	if audioSkipped {
		if err := r.skipExecutionStep(ctx, exec, payloadStep); err != nil {
			r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
			return false
		}
	} else if err := r.completeExecutionStep(ctx, exec, payloadStep); err != nil {
		r.failExecutionStep(ctx, exec, payloadStep, err)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, err)
		return false
	}

	return true
}
