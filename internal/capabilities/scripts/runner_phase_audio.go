package scriptgeneration

import (
	"context"
	"fmt"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
	OnPlanReady func(*GenerateResult) bool
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
			var audioAssets capabilityaudio.ResolvedAudioAssets
			var compileTimings AudioCompileTimings
			policy := req.MixPolicy
			if policy == "" {
				policy = capabilityaudio.MixVoiceoverWithDuckedClip
			}
			var clipAudioSource ClipAudioAssetSource
			if result.AudioPrefetch != nil && result.AudioPrefetch.ClipAudioSource != nil {
				// P1.1: use prefetched clip audio paths (already materialized
				// during TTS). The adapter serves cache hits without blocking.
				clipAudioSource = result.AudioPrefetch.ClipAudioSource
			} else if candidate, ok := r.audioAssetSource.(ClipAudioAssetSource); ok {
				clipAudioSource = candidate
			}
			// Clip audio is materialized in EVERY lane since the 2026-09-30
			// final-job fix: the remote master mixes the restored clip track, so
			// the compiled plan must carry verified local clip audio paths. The
			// old skip left AudioClip intents unresolvable here; the compile then
			// failed (or the projection dropped the track entirely).
			var clipPrepareMS int64
			clipPrepareMS, prepareErr := prepareClipAudioAssets(ctx, result, clipAudioSource, policy)
			if prepareErr != nil {
				cause := fmt.Errorf("prepare original clip audio failed: %w", prepareErr)
				r.failExecutionStep(ctx, exec, audioStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
			compileTimings.ClipAudioPrepareMS = clipPrepareMS
			// The audio intent block (BGM/SFX) is layered onto the same
			// VO-governed timeline: asset resolution → BGM windows → loop
			// expansion → SFX placement → automation, all compiled into the
			// sealed plan by CompileAudioWithIntents. Absent intents keep the
			// legacy primary-only CompileWithMixPolicy path.
			if len(req.BackgroundMusic) > 0 || len(req.SoundEffects) > 0 {
				audioSource := r.audioAssetSource
				// P1.1: use prefetched BGM/SFX paths (already resolved during TTS).
				if result.AudioPrefetch != nil && result.AudioPrefetch.AudioSource != nil {
					audioSource = result.AudioPrefetch.AudioSource
				}
				if audioSource == nil {
					cause := fmt.Errorf("audio intent block requires an audio asset resolver")
					r.failExecutionStep(ctx, exec, audioStep, cause)
					r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
					return false
				}
				audioInput := *result
				if req.FinalJob {
					audioInput = finalJobAudioInput(*result, req.SourceLanguage)
				}
				canonicalTimeline, compiledAudioPlan, audioAssets, compileTimings, err = CompileCanonicalAudioPlanAudioOnlyWithIntents(ctx, audioInput, req.SourceLanguage, capabilityaudio.DefaultAudioProfile(), audioSource, policy, req.BackgroundMusic, req.SoundEffects)
			} else {
				audioInput := *result
				if req.FinalJob {
					audioInput = finalJobAudioInput(*result, req.SourceLanguage)
				}
				canonicalTimeline, compiledAudioPlan, audioAssets, compileTimings, err = CompileCanonicalAudioPlanAudioOnly(audioInput, req.SourceLanguage, capabilityaudio.DefaultAudioProfile())
			}
			if err != nil {
				cause := fmt.Errorf("compile canonical audio plan failed: %w", err)
				r.failExecutionStep(ctx, exec, audioStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
			if req.FinalJob {
				// clips.v1 emits video on the 24 fps frame grid. The narration
				// duration can end between frames, so master the final audio to
				// the same rounded frame boundary. The renderer adds silence after
				// the last audio event; this preserves every spoken sample while
				// making the AAC stream long enough for copy-only packet mux.
				frameResolver, frameErr := capabilityaudio.NewFrameResolver(capabilityaudio.IntegerFrameRate(24))
				if frameErr == nil {
					compiledAudioPlan.MasterDurationUS, frameErr = frameResolver.FrameAlignedDurationUS(canonicalTimeline.DurationUS)
				}
				if frameErr == nil {
					frameErr = compiledAudioPlan.Seal()
				}
				if frameErr != nil {
					cause := fmt.Errorf("align final-job audio master to video frames: %w", frameErr)
					r.failExecutionStep(ctx, exec, audioStep, cause)
					r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
					return false
				}
			}
			// The compile returns its own subtimings; the clip-audio preparation
			// measured above is the owner of ClipAudioPrepareMS, so it is
			// re-applied instead of being silently replaced by the compile's
			// probe window.
			compileTimings = mergeAudioCompileTimings(compileTimings, clipPrepareMS)
			r.recordAudioCompileOperations(ctx, compileTimings)
			if result.ResolvedScenes, err = ResolveScenes(result.Scenes, req.SourceLanguage, mode, false); err != nil {
				cause := fmt.Errorf("resolve scenes for persistence failed: %w", err)
				r.failExecutionStep(ctx, exec, audioStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
			if out != nil && out.OnPlanReady != nil {
				result.CanonicalTimeline = &canonicalTimeline
				result.AudioPlan = &compiledAudioPlan
				if !r.compileAudioOverlayPlan(ctx, runID, req, exec, payloadStep, result) || !out.OnPlanReady(result) {
					return false
				}
				overlayPrepared = true
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
			// The joined finalize boundary checkpoints both certified siblings.
		} else if mode == capabilityaudio.AudioModeChunkedVoiceover {
			canonicalTimeline, err = CompileCanonicalTimeline(*result)
			if err != nil {
				cause := fmt.Errorf("compile canonical timeline failed: %w", err)
				r.failExecutionStep(ctx, exec, payloadStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
			if err := ValidateChunkedVoiceoversForLanguages(*result, req.VoiceoverLanguages); err != nil {
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
			scenesForPersistence := result.Scenes
			if req.FinalJob {
				scenesForPersistence = finalJobAudioInput(*result, req.SourceLanguage).Scenes
			}
			result.ResolvedScenes, err = ResolveScenes(scenesForPersistence, req.SourceLanguage, mode, false)
			if err != nil {
				cause := fmt.Errorf("resolve scenes for persistence failed: %w", err)
				r.failExecutionStep(ctx, exec, payloadStep, cause)
				r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
				return false
			}
		}
		result.CanonicalTimeline = &canonicalTimeline
		if !overlayPrepared && !r.compileAudioOverlayPlan(ctx, runID, req, exec, payloadStep, result) {
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

// logLocationOverlayPlan preserves producer-boundary diagnostics for both the
// serial and overlapping paths without compiling the immutable plan twice.
func (r *Runner) logLocationOverlayPlan(req GenerateRequest, result *GenerateResult) {
	// Keep the location path observable at the producer boundary. A place
	// may be extracted and geocoded successfully yet still disappear before
	// the worker sees its immutable render plan; recording the two counts
	// makes that boundary auditable from the run log.
	geocodedPlaces := 0
	for i := range result.Scenes {
		if ann := annotationsForLanguage(result.Scenes[i], req.SourceLanguage, result.SourceLanguage); ann != nil {
			for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), ann.PrimaryEntities...), ann.SecondaryEntities...) {
				if isPlaceEntityType(entity.Type) && entity.Geo != nil {
					geocodedPlaces++
				}
			}
		}
	}
	mapItems := 0
	if result.OverlayPlan != nil {
		for _, item := range result.OverlayPlan.Items {
			if item.Kind == "map" {
				mapItems++
			}
		}
	}
	spokenPlaceOccurrences := 0
	if result.EntityTimeline != nil {
		for _, scene := range result.EntityTimeline.Scenes {
			for _, entity := range scene.Entities {
				if isPlaceEntityType(entity.Type) {
					spokenPlaceOccurrences++
				}
			}
		}
	}
	matchedMapCandidates := 0
	validMapCandidates := 0
	placeMatchDebug := make([]string, 0, geocodedPlaces)
	if result.EntityTimeline != nil {
		for sceneIndex := range result.Scenes {
			scene := result.Scenes[sceneIndex]
			ann := annotationsForLanguage(scene, req.SourceLanguage, req.SourceLanguage)
			if ann == nil {
				continue
			}
			var timelineScene *capabilityentities.SceneEntityTimeline
			for i := range result.EntityTimeline.Scenes {
				if result.EntityTimeline.Scenes[i].SceneID == scene.ID {
					timelineScene = &result.EntityTimeline.Scenes[i]
					break
				}
			}
			for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), ann.PrimaryEntities...), ann.SecondaryEntities...) {
				if !isPlaceEntityType(entity.Type) || entity.Geo == nil {
					continue
				}
				matched := false
				if timelineScene != nil {
					matched = occurrenceFor(timelineScene.Entities, entity) != nil
				}
				if matched {
					matchedMapCandidates++
					occurrence := occurrenceFor(timelineScene.Entities, entity)
					if _, ok := capabilityoverlay.NewMapCandidate(occurrence.EntityID, entity.CanonicalName,
						entity.Geo.Latitude, entity.Geo.Longitude, occurrence.AudioStartUS,
						occurrence.AudioEndUS-occurrence.AudioStartUS, entity.Confidence, entity.Geo.Scope); ok {
						validMapCandidates++
					}
				}
				placeMatchDebug = append(placeMatchDebug, fmt.Sprintf("%s:%s/%s=%t", scene.ID, entity.Type, entity.CanonicalName, matched))
			}
		}
	}
	r.log.Info("location overlay plan compiled",
		zap.Int("geocoded_places", geocodedPlaces),
		zap.Int("spoken_place_occurrences", spokenPlaceOccurrences),
		zap.Int("matched_map_candidates", matchedMapCandidates),
		zap.Int("valid_map_candidates", validMapCandidates),
		zap.Strings("place_match_debug", placeMatchDebug),
		zap.Int("map_items", mapItems),
		zap.Bool("map_plate_resolver_wired", r.mapPlates != nil && r.shouldGeocodeScriptLocations(req)),
	)
}

// failAudioCompileStep records both the execution-step failure and the
// run-level retryable failure. All audio validation/rendering branches use the
// same fail-closed boundary, so it lives with the phase that owns the audio
// compile step instead of in a file of its own.
func (r *Runner) failAudioCompileStep(ctx context.Context, runID string, exec ExecutionContext, step ExecutionStep, cause error) bool {
	r.failExecutionStep(ctx, exec, step, cause)
	r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
	return false
}
