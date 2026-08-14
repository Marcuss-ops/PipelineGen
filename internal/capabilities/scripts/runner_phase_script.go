package scriptgeneration

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

func (r *Runner) runSceneTextPhase(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, run *GenerationRun, resumeIdx int) (*GenerateResult, bool) {
	// ── Stage 2: Generate Scene Text ─────────────────────────────
	scriptStep, startErr := r.startExecutionStep(ctx, exec, "SCRIPT", "generation")
	if startErr != nil {
		r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, startErr)
		return nil, false
	}
	var result *GenerateResult
	scriptSkipped := stageSkipped(resumeIdx, StageGeneratingSceneText)
	if !scriptSkipped {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageGeneratingSceneText); err != nil {
			r.failExecutionStep(ctx, exec, scriptStep, err)
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, err)
			return result, false
		}
		scenes, err := r.textGen.GenerateSceneText(ctx, req)
		if err != nil {
			cause := fmt.Errorf("generate scene text failed: %w", err)
			r.failExecutionStep(ctx, exec, scriptStep, cause)
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
			return result, false
		}
		if len(scenes) == 0 {
			cause := fmt.Errorf("generate scene text returned zero scenes")
			r.failExecutionStep(ctx, exec, scriptStep, cause)
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
			return result, false
		}
		result = &GenerateResult{Scenes: scenes, Title: req.Title, OutputName: req.OutputName, VoiceoverGroup: req.ScriptParams.VoiceoverGroup}
		r.checkpoint(ctx, runID, result)
		if err := r.emitSceneCommits(ctx, runID, req, exec, scenes); err != nil {
			cause := fmt.Errorf("emit scene commits: %w", err)
			r.failExecutionStep(ctx, exec, scriptStep, cause)
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
			return result, false
		}
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageGeneratingSceneText)))
	} else {
		r.log.Info("skipping completed stage", zap.String("stage", string(StageGeneratingSceneText)))
		// Load result from repo if available.
		if run != nil && run.Result != nil {
			result = run.Result
		}
	}

	// Record resolved clip assets as script inputs once the scene plan exists.
	if result != nil {
		ordinal := 0
		for _, scene := range result.Scenes {
			if scene.Clip != nil {
				if err := r.attachInputAsset(ctx, exec, scriptStep.StepID, scene.Clip.ID, ordinal); err != nil {
					r.failExecutionStep(ctx, exec, scriptStep, err)
					r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, err)
					return result, false
				}
				ordinal++
			}
		}
	}
	if scriptSkipped {
		if err := r.skipExecutionStep(ctx, exec, scriptStep); err != nil {
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, err)
			return result, false
		}
	} else if err := r.completeExecutionStep(ctx, exec, scriptStep); err != nil {
		r.failExecutionStep(ctx, exec, scriptStep, err)
		r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, err)
		return result, false
	}

	// Nil guard: result must be non-nil before downstream stages.
	if result == nil {
		result = &GenerateResult{Scenes: []Scene{}, Title: req.Title, OutputName: req.OutputName, VoiceoverGroup: req.ScriptParams.VoiceoverGroup}
	}

	return result, true
}

// emitSceneCommits publishes one SceneCommitted event per stable scene after
// the scene-text stage completes. It is a no-op when no observer is wired.
// Emission happens only on the fresh-generation path (not on resume), so a
// committed scene is reported exactly once per generation attempt.
func (r *Runner) emitSceneCommits(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, scenes []Scene) error {
	if r.sceneCommitObserver == nil {
		return nil
	}
	for _, scene := range scenes {
		event := NewSceneCommitted(runID, scene, req.SourceLanguage, int64(exec.Attempt))
		if err := r.sceneCommitObserver.OnSceneCommitted(ctx, event); err != nil {
			return fmt.Errorf("scene %q commit: %w", scene.ID, err)
		}
	}
	return nil
}
