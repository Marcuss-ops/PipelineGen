package scriptgeneration

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	"strings"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func (r *Runner) runVoiceoverPhase(ctx context.Context, runID string, req GenerateRequest, routing kernelscript.ArtifactRoutingContext, exec ExecutionContext, resumeIdx int, result *GenerateResult) bool {
	// ── Stage 4: Generate Voiceovers (scene-level idempotent) ───
	voiceoverStep, startErr := r.startExecutionStep(ctx, exec, "VOICEOVER", "audio")
	if startErr != nil {
		r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, startErr)
		return false
	}
	// Voiceover generation is gated on the resolved audio mode: only
	// CHUNKED_VOICEOVER and COMBINED_TIMELINE produce voiceover assets.
	// audio.mode NONE (or omitted → NONE) must not trigger TTS nor
	// stage/publish voiceover artifacts — a timeline-only run stays
	// metadata-only and a plain script run pays no unrequested TTS cost.
	mode, modeErr := capabilityaudio.ResolveAudioMode(req.Audio, false)
	if modeErr != nil {
		// Envelope validation and the builder reject invalid audio-mode
		// combinations earlier; fail closed here for direct-runner callers.
		cause := fmt.Errorf("voiceover phase: resolve audio mode: %w", modeErr)
		r.failExecutionStep(ctx, exec, voiceoverStep, cause)
		r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, cause)
		return false
	}
	needsVoiceover := mode == capabilityaudio.AudioModeChunkedVoiceover || mode == capabilityaudio.AudioModeCombinedTimeline
	// godlike/07 NO-FAKE-AVAILABILITY: fail BEFORE the first TTS call when a
	// voiceover-producing mode is active but no Project was resolved. The
	// publisher already fail-closes on an empty Project
	// (ErrVoiceoverPublishProjectRequired); this gate moves that failure to
	// the start of the phase so no TTS work is wasted and no "scene"
	// namespace is silently invented.
	if needsVoiceover && routing.Project == "" {
		cause := fmt.Errorf("%w: voiceover publishing requires a resolved Project", ErrProjectRequired)
		r.failExecutionStep(ctx, exec, voiceoverStep, cause)
		r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, cause)
		return false
	}
	// On retry, scenes that already have a voiceover for a language
	// are skipped. The Upsert-style DocumentPublisher ensures docs
	// are not duplicated either.
	voiceoverSkipped := stageSkipped(resumeIdx, StageGeneratingVoiceovers) || r.voiceoverGen == nil || !needsVoiceover
	if !voiceoverSkipped {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageGeneratingVoiceovers); err != nil {
			r.failExecutionStep(ctx, exec, voiceoverStep, err)
			r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, err)
			return false
		}
		ttsStarted := time.Now()
		ttsCalls := 0
		if result.AudioMetrics == nil {
			result.AudioMetrics = &AudioPipelineMetrics{}
		}
		for i := range result.Scenes {
			scene := &result.Scenes[i]
			for lang, text := range scene.Text {
				if text == "" {
					continue
				}
				// Scene-level idempotency: skip if voiceover already exists.
				if existing, ok := scene.Voiceover[lang]; ok && existing.ID != "" {
					r.log.Debug("skipping already generated voiceover",
						zap.String("scene_id", scene.ID),
						zap.String("language", string(lang)),
					)
					continue
				}
				sceneTTSStarted := time.Now()
				audioRef, err := r.voiceoverGen.Generate(ctx, VoiceoverInput{
					SceneID:  scene.ID,
					Language: lang,
					Text:     text,
					// Project is the canonical semantic project namespace
					// resolved ONCE by resolveArtifactRoutingContext at
					// generation start and propagated verbatim to the
					// per-item pipeline so the voiceover publish satisfies
					// the semantic publish contract
					// (PR-VOICEOVER-DRIVE-DRIFT: Project is required). It is
					// guaranteed non-empty here by the phase-level fail-fast
					// gate above.
					Project: routing.Project,
					// VoiceoverFolderID is the caller-explicit Drive folder for
					// voiceover artifacts, resolved ONCE by
					// resolveArtifactRoutingContext (output.voiceover_folder_id;
					// empty falls back to the configured default). Forwarded
					// verbatim so the per-scene TTS command never replaces a
					// caller-explicit destination with the default folder.
					VoiceoverFolderID: routing.VoiceoverFolderID,
					// Forward the request-level timing policy so the per-item
					// pipeline can honour required/best-effort fail-closed
					// semantics (missing/invalid timing fails the job instead of
					// producing plausible-but-wrong timestamps).
					Timing: req.Timing,
				})
				if err != nil {
					cause := fmt.Errorf("voiceover generation for scene %s lang %s failed: %w", scene.ID, lang, err)
					r.failExecutionStep(ctx, exec, voiceoverStep, cause)
					r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, cause)
					return false
				}
				ttsCalls++
				result.AudioMetrics.TTSScenes = append(result.AudioMetrics.TTSScenes, TTSSSceneMetric{SceneID: scene.ID, Language: lang, DurationMS: time.Since(sceneTTSStarted).Milliseconds(), Characters: len([]rune(text)), Words: len(strings.Fields(text)), OutputDurationMS: time.Duration(audioRef.Duration * float64(time.Second)).Milliseconds()})
				if scene.Voiceover == nil {
					scene.Voiceover = make(map[Language]AudioReference)
				}
				scene.Voiceover[lang] = audioRef
				if err := r.attachOutputAsset(ctx, exec, voiceoverStep.StepID, audioRef.ID, ttsCalls-1); err != nil {
					r.failExecutionStep(ctx, exec, voiceoverStep, err)
					r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, err)
					return false
				}
				// Audio-only scenes are narration-driven even when a clip is
				// attached as evidence. The clip total/used duration remains
				// metadata; it must never stretch the audio master.
				if (mode == capabilityaudio.AudioModeCombinedTimeline || scene.Clip == nil) && audioRef.Duration > 0 {
					scene.DurationMS = int64(audioRef.Duration*1000 + 0.5)
					scene.DurationUS = int64(audioRef.Duration*1_000_000 + 0.5)
				}
			}
			r.checkpoint(ctx, runID, result)
		}
		result.AudioMetrics.TTSMS += time.Since(ttsStarted).Milliseconds()
		result.AudioMetrics.TTSCalls += ttsCalls
		r.recordVoiceoverOperation(ctx, result.AudioMetrics.TTSMS)
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageGeneratingVoiceovers)))
	}
	if voiceoverSkipped {
		if err := r.skipExecutionStep(ctx, exec, voiceoverStep); err != nil {
			r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, err)
			return false
		}
	} else if err := r.completeExecutionStep(ctx, exec, voiceoverStep); err != nil {
		r.failExecutionStep(ctx, exec, voiceoverStep, err)
		r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, err)
		return false
	}

	return true
}
