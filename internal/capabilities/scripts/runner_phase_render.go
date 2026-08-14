package scriptgeneration

import (
	"context"
	"fmt"
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"go.uber.org/zap"
	"time"
)

func (r *Runner) runRenderPayloadPhase(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, resumeIdx int, result *GenerateResult) bool {
	// ── Build Render Payload (before document publication) ───────
	payloadStep, startErr := r.startExecutionStep(ctx, exec, "RENDER_PLAN", "render")
	if startErr != nil {
		r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, startErr)
		return false
	}
	var canonicalTimeline capabilityaudio.CanonicalTimeline
	var compiledAudioPlan capabilityaudio.CompiledAudioPlan
	payloadSkipped := stageSkipped(resumeIdx, StageBuildingRenderPayload) || !req.RenderVideo
	if !payloadSkipped {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageBuildingRenderPayload); err != nil {
			r.failExecutionStep(ctx, exec, payloadStep, err)
			r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, err)
			return false
		}
		// Audio mode is an explicit request-level choice. The presence of
		// generated scenes (or voiceover assets) is never a mode selector.
		mode, err := capabilityaudio.ResolveAudioMode(req.Audio, false, req.RenderVideo)
		if err != nil {
			r.failExecutionStep(ctx, exec, payloadStep, err)
			r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, err)
			return false
		}
		result.AudioMode = mode
		if mode != capabilityaudio.AudioModeCombinedTimeline && result.FinalAudio != nil {
			cause := fmt.Errorf("%s must not carry final audio", mode)
			r.failExecutionStep(ctx, exec, payloadStep, cause)
			r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, cause)
			return false
		}
		if mode == capabilityaudio.AudioModeCombinedTimeline {
			audioStep, startErr := r.startExecutionStep(ctx, exec, "AUDIO_COMPILE", "audio")
			if startErr != nil {
				r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, startErr)
				return false
			}
			if r.combinedAudioRenderer == nil {
				cause := fmt.Errorf("COMBINED_TIMELINE requires a CombinedAudioRenderer")
				r.failExecutionStep(ctx, exec, audioStep, cause)
				r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, cause)
				return false
			}
			started := time.Now()
			var audioAssets capabilityaudio.ResolvedAudioAssets
			var compileTimings AudioCompileTimings
			canonicalTimeline, compiledAudioPlan, audioAssets, compileTimings, err = CompileCanonicalAudioPlanWithTimings(*result, req.SourceLanguage, capabilityaudio.DefaultAudioProfile())
			if err != nil {
				cause := fmt.Errorf("compile canonical audio plan failed: %w", err)
				r.failExecutionStep(ctx, exec, audioStep, cause)
				r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, cause)
				return false
			}
			if result.ResolvedScenes, err = ResolveScenes(result.Scenes, req.SourceLanguage); err != nil {
				cause := fmt.Errorf("resolve scenes for persistence failed: %w", err)
				r.failExecutionStep(ctx, exec, audioStep, cause)
				r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, cause)
				return false
			}
			var finalAudio FinalAudioReference
			var metrics AudioPipelineMetrics
			if result.FinalAudio != nil && ValidateFinalAudioReference(*result.FinalAudio, compiledAudioPlan) == nil {
				// A checkpointed certified artifact is the idempotency boundary.
				// Do not invoke TTS/mix/encode again on a retry.
				finalAudio = *result.FinalAudio
				if result.AudioMetrics != nil {
					metrics = *result.AudioMetrics
				}
			} else {
				finalAudio, metrics, err = r.combinedAudioRenderer.Render(ctx, compiledAudioPlan, audioAssets)
				if err != nil {
					cause := fmt.Errorf("combined audio render failed: %w", err)
					r.failExecutionStep(ctx, exec, audioStep, cause)
					r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, cause)
					return false
				}
				metrics.TimelineCompileMS = compileTimings.TimelineCompileMS
				metrics.AudioPlanCompileMS = compileTimings.AudioPlanCompileMS
				metrics.ClipAudioPrepareMS = compileTimings.ClipAudioPrepareMS
				if err := ValidateFinalAudioReference(finalAudio, compiledAudioPlan); err != nil {
					cause := fmt.Errorf("final audio certification failed: %w", err)
					r.failExecutionStep(ctx, exec, audioStep, cause)
					r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, cause)
					return false
				}
			}
			// Cert-time invariant: the recorded VO source_duration_us must match
			// the certified probe durations (modulo the scene-window clamp).
			// Runs for both freshly rendered and checkpointed final audio.
			if err := ValidateVoiceoverSourceDurations(*result, req.SourceLanguage, canonicalTimeline, compiledAudioPlan); err != nil {
				cause := fmt.Errorf("voiceover source-duration certification failed: %w", err)
				r.failExecutionStep(ctx, exec, audioStep, cause)
				r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, cause)
				return false
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
				r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, err)
				return false
			}
			if err := r.recordExecutionMetric(ctx, exec, audioStep.StepID, "audio_duration_ms", float64(finalAudio.DurationMS), "ms"); err != nil {
				r.failExecutionStep(ctx, exec, audioStep, err)
				r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, err)
				return false
			}
			if err := r.completeExecutionStep(ctx, exec, audioStep); err != nil {
				r.failExecutionStep(ctx, exec, audioStep, err)
				r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, err)
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
				r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, cause)
				return false
			}
			if err := ValidateChunkedVoiceovers(*result); err != nil {
				r.failExecutionStep(ctx, exec, payloadStep, err)
				r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, err)
				return false
			}
			result.AudioStrategy = capabilityaudio.TimelineMix
		} else if mode == capabilityaudio.AudioModeNone {
			canonicalTimeline, err = CompileCanonicalTimeline(*result)
			if err != nil {
				cause := fmt.Errorf("compile canonical timeline failed: %w", err)
				r.failExecutionStep(ctx, exec, payloadStep, cause)
				r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, cause)
				return false
			}
		}
		if len(result.ResolvedScenes) == 0 {
			result.ResolvedScenes, err = ResolveScenes(result.Scenes, req.SourceLanguage)
			if err != nil {
				cause := fmt.Errorf("resolve scenes for persistence failed: %w", err)
				r.failExecutionStep(ctx, exec, payloadStep, cause)
				r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, cause)
				return false
			}
		}
		result.CanonicalTimeline = &canonicalTimeline
		frameRate := capabilityaudio.IntegerFrameRate(30)
		if req.RenderFrameRate != nil {
			frameRate = *req.RenderFrameRate
		}
		renderPlan, err := CompileCanonicalRenderPlanWithFrameRate(*result, canonicalTimeline, runID, "generation.v1", frameRate)
		if err != nil {
			cause := fmt.Errorf("compile render plan failed: %w", err)
			r.failExecutionStep(ctx, exec, payloadStep, cause)
			r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, cause)
			return false
		}
		result.RenderPlan = renderPlan
		if err := r.recordExecutionMetric(ctx, exec, payloadStep.StepID, "render_plan_duration_frames", float64(renderPlan.DurationFrames), "frames"); err != nil {
			r.failExecutionStep(ctx, exec, payloadStep, err)
			r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, err)
			return false
		}
		r.checkpoint(ctx, runID, result)
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageBuildingRenderPayload)), zap.String("audio_mode", string(mode)), zap.String("render_plan_sha256", renderPlan.PlanSHA256))
	}
	if payloadSkipped {
		if err := r.skipExecutionStep(ctx, exec, payloadStep); err != nil {
			r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, err)
			return false
		}
	} else if err := r.completeExecutionStep(ctx, exec, payloadStep); err != nil {
		r.failExecutionStep(ctx, exec, payloadStep, err)
		r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, err)
		return false
	}

	return true
}
