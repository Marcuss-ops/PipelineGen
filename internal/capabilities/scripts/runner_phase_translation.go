package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptRunner "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/runner"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

type translationWork struct {
	sceneIndex int
	lang       contract.Language
	text       string
}

func sortTranslationWork(work []translationWork, source contract.Language, targets []contract.Language) {
	scriptRunner.SortBySceneAndLanguage(work, func(item translationWork) int { return item.sceneIndex }, func(item translationWork) string { return string(item.lang) }, string(source), languageStrings(targets))
}

func (r *Runner) runTranslationPhase(ctx context.Context, runID string, req contract.GenerateRequest, exec ExecutionContext, resumeIdx int, result *contract.GenerateResult) bool {
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
			r.failExecutionStepBestEffort(ctx, exec, translationStep, err)
			r.failRunWithRetry(ctx, runID, StageTranslatingScenes, err)
			return false
		}
		work := make([]translationWork, 0, len(result.Scenes)*len(req.Languages))
		for i := range result.Scenes {
			fixedDisplay := result.Scenes[i].ExecutionMode.IsFixedMedia()
			if !result.Scenes[i].ExecutionMode.AllowsTranslation() && !fixedDisplay {
				continue
			}
			if fixedDisplay && !result.Scenes[i].ExecutionMode.AllowsDisplayTextTranslation() {
				continue
			}
			for _, lang := range req.Languages {
				if lang == req.SourceLanguage || result.Scenes[i].Text[lang] != "" {
					continue
				}
				sourceText := result.Scenes[i].Text[req.SourceLanguage]
				if sourceText == "" {
					continue
				}
				// The envelope is the first authority; when it lost this unit,
				// the per-artifact row restores it without a translator call.
				if restored, ok := r.loadTranslationCheckpoint(ctx, runID, result.Scenes[i].ID, req.SourceLanguage, lang, sourceText); ok {
					if result.Scenes[i].Text == nil {
						result.Scenes[i].Text = make(map[contract.Language]string)
					}
					result.Scenes[i].Text[lang] = restored
					continue
				}
				work = append(work, translationWork{sceneIndex: i, lang: lang, text: sourceText})
			}
		}
		// Deterministic dispatch priority: (scene_index, language_priority).
		// The provider calls stay concurrent, but the order in which units are
		// offered to the pool is pinned so the scheduler never dispatches
		// scene×language work in an arbitrary order across runs.
		// Stable sorting preserves insertion order for duplicate/equal-priority
		// work while delegating the canonical language rank to scripts/runner.
		sortTranslationWork(work, req.SourceLanguage, req.Languages)
		workers := r.translationConcurrency
		if req.TranslationConcurrency > 0 {
			workers = req.TranslationConcurrency
		}
		if workers <= 0 {
			workers = DefaultTranslationConcurrency
		}
		// Each unit is applied to result and checkpointed IMMEDIATELY as it
		// completes (guarded by applyMu) — never after the whole fan-out — so
		// a crash mid-phase (kill -9) preserves the already-translated scenes.
		// On restart the restored partial result makes those scenes REUSE, the
		// in-flight scene RETRY, and the remaining scenes CONTINUE (resume
		// reale: no full restart from scene 1).
		applier := &translationApplier{runner: r, runID: runID, exec: exec, req: req, result: result}
		// §7.3 trace: the translation path has no per-scene ready boundary (every
		// scene becomes ready at once), so ReadyAt stays zero; the queue wait is
		// the real dispatch→start interval inside the translation pool.
		enqueuedAt := time.Now().UTC()
		var err error
		if batcher, ok := r.translator.(BatchTranslator); ok && len(work) > 1 {
			// WST-04: one provider request per (language, chunk) instead of one
			// per scene. The per-scene path below remains the fallback for a
			// translator without the batched capability and for a chunk the
			// provider answers unusably.
			err = r.runTranslationBatches(ctx, applier, batcher, work, workers, enqueuedAt)
		} else {
			_, err = concurrent.Map(ctx, work, workers, func(opCtx context.Context, _ int, item translationWork) (struct{}, error) {
				value, translateErr := r.translateUnit(opCtx, applier, item, result.Scenes[item.sceneIndex].ID, enqueuedAt)
				if translateErr != nil {
					return struct{}{}, translateErr
				}
				return struct{}{}, applier.apply(opCtx, item, value)
			})
		}
		// Flush the newest complete result after all workers have joined. This
		// closes the debounce window for normal completion and preserves all
		// successful units even when another worker failed.
		if snapshot, snapshotErr := detachCheckpointResult(result); snapshotErr != nil {
			r.log.Warn("translation final checkpoint snapshot failed", zap.String("run_id", runID), zap.Error(snapshotErr))
		} else if snapshot != nil {
			recordCheckpointFlush()
			func() {
				defer applier.gate.complete()
				r.checkpoint(ctx, runID, snapshot)
			}()
		}
		if err != nil {
			r.failExecutionStepBestEffort(ctx, exec, translationStep, err)
			r.failRunWithRetry(ctx, runID, StageTranslatingScenes, err)
			return false
		}
		var summary kernobs.OperationSummary
		if run := kernobs.FromContext(ctx); run != nil {
			summary = kernobs.SummarizeOperations(run.Report(), "translation", "translate")
		}
		if summary.Calls == 0 {
			// A runner without a bound observability run is a supported test and
			// dry-run mode. The phase's own counter still reports the provider
			// round-trips actually issued (batches + per-scene fallbacks), so a
			// batched run does not inflate the count back to scene×language.
			summary.Calls = applier.calls.Load()
			if summary.Calls == 0 {
				summary.Calls = int64(len(work))
			}
		}
		result.TranslationMetrics = &contract.TranslationPipelineMetrics{Calls: int(summary.Calls), Concurrency: workers, WallMS: summary.WallMs}
		if err := validateTranslationCoverage(result, req); err != nil {
			r.failExecutionStepBestEffort(ctx, exec, translationStep, err)
			r.failRunWithRetry(ctx, runID, StageTranslatingScenes, err)
			return false
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
				r.failExecutionStepBestEffort(ctx, exec, translationStep, err)
				r.failRunWithRetry(ctx, runID, StageTranslatingScenes, err)
				return false
			}
		}
		r.log.Info("translation fanout complete",
			zap.String("run_id", runID),
			zap.Int("calls", int(summary.Calls)),
			zap.Int("concurrency", workers),
			zap.Int64("wall_ms", summary.WallMs),
		)
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageTranslatingScenes)))
	}
	if translationSkipped {
		if err := r.skipExecutionStep(ctx, exec, translationStep); err != nil {
			r.failRunWithRetry(ctx, runID, StageTranslatingScenes, err)
			return false
		}
	} else if err := r.completeExecutionStep(ctx, exec, translationStep); err != nil {
		r.failExecutionStepBestEffort(ctx, exec, translationStep, err)
		r.failRunWithRetry(ctx, runID, StageTranslatingScenes, err)
		return false
	}

	return true
}

// ErrTranslationIncomplete is the typed failure for a translation phase that
// completed without producing every requested target. It is deliberately a
// postcondition, not a provider error: R108 reached SUCCEEDED with zero calls
// and source text tagged as the target language.
var ErrTranslationIncomplete = errors.New("scriptgeneration: translation coverage incomplete")

// validateTranslationCoverage proves the translation phase delivered what the
// request asked for. A target language is covered only when every translatable
// scene carries a non-empty target text that differs from the source text.
// Fixed-display scenes are included because their display text is translated
// for subtitle/caption burn.
func validateTranslationCoverage(result *contract.GenerateResult, req contract.GenerateRequest) error {
	if result == nil || len(req.Languages) == 0 {
		return nil
	}
	var gaps []string
	for i := range result.Scenes {
		scene := &result.Scenes[i]
		fixedDisplay := scene.ExecutionMode.IsFixedMedia()
		if !scene.ExecutionMode.AllowsTranslation() && !fixedDisplay {
			continue
		}
		if fixedDisplay && !scene.ExecutionMode.AllowsDisplayTextTranslation() {
			continue
		}
		sourceText := strings.TrimSpace(scene.Text[req.SourceLanguage])
		if sourceText == "" {
			// A scene without source text never entered translation work.
			continue
		}
		for _, lang := range req.Languages {
			if lang == req.SourceLanguage {
				continue
			}
			translated := strings.TrimSpace(scene.Text[lang])
			switch {
			case translated == "":
				gaps = append(gaps, fmt.Sprintf("scene %s: missing %s translation", scene.ID, lang))
			case len([]rune(sourceText)) >= 20 && normalizeTranslationText(translated) == normalizeTranslationText(sourceText):
				gaps = append(gaps, fmt.Sprintf("scene %s: %s translation is identical to the %s source", scene.ID, lang, req.SourceLanguage))
			}
		}
	}
	if len(gaps) == 0 {
		return nil
	}
	const maxReported = 8
	reported := gaps
	if len(reported) > maxReported {
		reported = reported[:maxReported]
	}
	suffix := ""
	if len(gaps) > len(reported) {
		suffix = fmt.Sprintf("; ... and %d more", len(gaps)-len(reported))
	}
	return fmt.Errorf("%w: %s%s", ErrTranslationIncomplete, strings.Join(reported, "; "), suffix)
}

// normalizeTranslationText compares translations structurally: case and
// repeated whitespace are not a translation.
func normalizeTranslationText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}
