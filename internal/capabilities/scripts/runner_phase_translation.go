package scriptgeneration

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

func (r *Runner) runTranslationPhase(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, resumeIdx int, result *GenerateResult) bool {
	// ── Stage 3: Translate Scenes (scene-level idempotent) ───────
	translationStep, startErr := r.startExecutionStep(ctx, exec, "TRANSLATION", "generation")
	if startErr != nil {
		r.failRunWithRetry(ctx, runID, StageTranslatingScenes, startErr)
		return false
	}
	// On retry, scenes that already have translated text for a target
	// language are skipped. The checkpoint after each scene ensures
	// partial progress is preserved.
	translationSkipped := stageSkipped(resumeIdx, StageTranslatingScenes) || len(req.Languages) == 0
	if !translationSkipped {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageTranslatingScenes); err != nil {
			r.failExecutionStep(ctx, exec, translationStep, err)
			r.failRunWithRetry(ctx, runID, StageTranslatingScenes, err)
			return false
		}
		for _, lang := range req.Languages {
			if lang == req.SourceLanguage {
				continue
			}
			for i := range result.Scenes {
				// Scene-level idempotency: skip if already translated.
				if result.Scenes[i].Text[lang] != "" {
					r.log.Debug("skipping already translated scene",
						zap.String("scene_id", result.Scenes[i].ID),
						zap.String("language", string(lang)),
					)
					continue
				}
				sourceText := result.Scenes[i].Text[req.SourceLanguage]
				if sourceText == "" {
					continue
				}
				translated, err := r.translator.Translate(ctx, TranslationInput{
					SceneID:        result.Scenes[i].ID,
					SourceLanguage: req.SourceLanguage,
					TargetLanguage: lang,
					SourceText:     sourceText,
				})
				if err != nil {
					cause := fmt.Errorf("translate scene %s to %s failed: %w", result.Scenes[i].ID, lang, err)
					r.failExecutionStep(ctx, exec, translationStep, cause)
					r.failRunWithRetry(ctx, runID, StageTranslatingScenes, cause)
					return false
				}
				if result.Scenes[i].Text == nil {
					result.Scenes[i].Text = make(map[Language]string)
				}
				result.Scenes[i].Text[lang] = translated
				// Checkpoint after each translated scene preserves progress.
				r.checkpoint(ctx, runID, result)
			}
		}
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageTranslatingScenes)))
	}
	if translationSkipped {
		if err := r.skipExecutionStep(ctx, exec, translationStep); err != nil {
			r.failRunWithRetry(ctx, runID, StageTranslatingScenes, err)
			return false
		}
	} else if err := r.completeExecutionStep(ctx, exec, translationStep); err != nil {
		r.failExecutionStep(ctx, exec, translationStep, err)
		r.failRunWithRetry(ctx, runID, StageTranslatingScenes, err)
		return false
	}

	return true
}
