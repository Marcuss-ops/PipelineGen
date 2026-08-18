package scriptgeneration

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

type translationWork struct {
	sceneIndex int
	lang       Language
	text       string
}

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
		work := make([]translationWork, 0, len(result.Scenes)*len(req.Languages))
		for i := range result.Scenes {
			for _, lang := range req.Languages {
				if lang == req.SourceLanguage || result.Scenes[i].Text[lang] != "" {
					continue
				}
				if sourceText := result.Scenes[i].Text[req.SourceLanguage]; sourceText != "" {
					work = append(work, translationWork{sceneIndex: i, lang: lang, text: sourceText})
				}
			}
		}
		// Deterministic dispatch priority: (scene_index, language_priority).
		// The provider calls stay concurrent, but the order in which units are
		// offered to the pool is pinned so the scheduler never dispatches
		// scene×language work in an arbitrary order across runs.
		sort.SliceStable(work, func(a, b int) bool {
			if work[a].sceneIndex != work[b].sceneIndex {
				return work[a].sceneIndex < work[b].sceneIndex
			}
			return dispatchLanguagePriority(req.SourceLanguage, req.Languages, work[a].lang) <
				dispatchLanguagePriority(req.SourceLanguage, req.Languages, work[b].lang)
		})
		workers := r.translationConcurrency
		if workers <= 0 {
			workers = DefaultTranslationConcurrency
		}
		started := time.Now()
		// Each unit is applied to result and checkpointed IMMEDIATELY as it
		// completes (guarded by applyMu) — never after the whole fan-out — so
		// a crash mid-phase (kill -9) preserves the already-translated scenes.
		// On restart the restored partial result makes those scenes REUSE, the
		// in-flight scene RETRY, and the remaining scenes CONTINUE (resume
		// reale: no full restart from scene 1).
		var applyMu sync.Mutex
		_, err := concurrent.Map(ctx, work, workers, func(opCtx context.Context, _ int, item translationWork) (struct{}, error) {
			var value string
			translateErr := kernobs.MeasureOperation(opCtx, kernobs.OperationInfo{
				Stage: "translation", Component: "translator", Operation: "translate", Provider: string(item.lang),
			}, func(measureCtx context.Context) error {
				var err error
				value, err = r.translator.Translate(measureCtx, TranslationInput{
					SceneID: result.Scenes[item.sceneIndex].ID, SourceLanguage: req.SourceLanguage,
					TargetLanguage: item.lang, SourceText: item.text,
				})
				return err
			})
			if translateErr != nil {
				return struct{}{}, fmt.Errorf("translate scene %s to %s failed: %w", result.Scenes[item.sceneIndex].ID, item.lang, translateErr)
			}
			applyMu.Lock()
			if result.Scenes[item.sceneIndex].Text == nil {
				result.Scenes[item.sceneIndex].Text = make(map[Language]string)
			}
			result.Scenes[item.sceneIndex].Text[item.lang] = value
			r.checkpoint(ctx, runID, result)
			applyMu.Unlock()
			return struct{}{}, nil
		})
		if err != nil {
			r.failExecutionStep(ctx, exec, translationStep, err)
			r.failRunWithRetry(ctx, runID, StageTranslatingScenes, err)
			return false
		}
		result.TranslationMetrics = &TranslationPipelineMetrics{
			Calls: len(work), Concurrency: workers, WallMS: time.Since(started).Milliseconds(),
		}
		// Per-(scene, language) translation correlation: record each target
		// translation so "Spanish Scene 4" is traceable to this operation.
		for _, item := range work {
			if err := r.recordArtifactOperation(ctx, exec, ArtifactOperation{
				OperationID: artifactOperationID(exec.Attempt, OperationTranslation, result.Scenes[item.sceneIndex].ID, string(item.lang)),
				Kind:        OperationTranslation,
				SceneID:     result.Scenes[item.sceneIndex].ID,
				Language:    item.lang,
				Status:      "COMPLETED",
			}); err != nil {
				r.failExecutionStep(ctx, exec, translationStep, err)
				r.failRunWithRetry(ctx, runID, StageTranslatingScenes, err)
				return false
			}
		}
		r.log.Info("translation fanout complete",
			zap.String("run_id", runID),
			zap.Int("calls", len(work)),
			zap.Int("concurrency", workers),
			zap.Int64("wall_ms", time.Since(started).Milliseconds()),
		)
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
