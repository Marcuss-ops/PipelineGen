package scriptgeneration

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	"strings"
	"time"
)

func (r *Runner) runVoiceoverPhase(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, resumeIdx int, result *GenerateResult) bool {
	// ── Stage 4: Generate Voiceovers (scene-level idempotent) ───
	voiceoverStep, startErr := r.startExecutionStep(ctx, exec, "VOICEOVER", "audio")
	if startErr != nil {
		r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, startErr)
		return false
	}
	// On retry, scenes that already have a voiceover for a language
	// are skipped. The Upsert-style DocumentPublisher ensures docs
	// are not duplicated either.
	voiceoverSkipped := stageSkipped(resumeIdx, StageGeneratingVoiceovers) || r.voiceoverGen == nil
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
				// Voiceover duration is the authoritative duration for
				// narration-only timeline segments. Clip-bound scenes retain
				// their source-range duration so audio and video remain
				// aligned by construction.
				if scene.Clip == nil && audioRef.Duration > 0 {
					scene.DurationMS = int64(audioRef.Duration*1000 + 0.5)
				}
			}
			r.checkpoint(ctx, runID, result)
		}
		result.AudioMetrics.TTSMS += time.Since(ttsStarted).Milliseconds()
		result.AudioMetrics.TTSCalls += ttsCalls
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
