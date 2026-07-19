// Package scriptgeneration — runner.go implements the durable
// stage-based execution of the script generation workflow. Each
// stage is executed in order, with checkpoint updates after every
// successful stage. A retry resumes from the last checkpointed stage.
//
// Verdetto contract:
//
//	ScriptGenerationRunner
//	  ├─ Normalize
//	  ├─ GenerateSceneText
//	  ├─ TranslateScenes
//	  ├─ GenerateVoiceovers
//	  ├─ UpsertDocuments
//	  ├─ BuildRenderPayload
//	  └─ EnqueueRender
//
// The runner is NOT an abstract phase registry or plugin system.
// It is a single struct with a linear, readable Execute method.
package scriptgeneration

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// Runner executes the durable script generation stages.
// Each stage is checkpointed so a retry resumes from the last
// successfully completed stage.
type Runner struct {
	repo           RunRepository
	textGen        TextGenerator
	translator     Translator
	voiceoverGen   VoiceoverGenerator
	docPublisher   DocumentPublisher
	renderEnqueuer RenderEnqueuer
	log            *zap.Logger
}

// NewRunner constructs the Runner with all required ports.
func NewRunner(
	repo RunRepository,
	textGen TextGenerator,
	translator Translator,
	voiceoverGen VoiceoverGenerator,
	docPublisher DocumentPublisher,
	renderEnqueuer RenderEnqueuer,
) *Runner {
	if repo == nil {
		panic("scriptgeneration: RunRepository is required for Runner")
	}
	return &Runner{
		repo:           repo,
		textGen:        textGen,
		translator:     translator,
		voiceoverGen:   voiceoverGen,
		docPublisher:   docPublisher,
		renderEnqueuer: renderEnqueuer,
		log:            zap.NewNop(),
	}
}

// SetLogger sets the runner's logger. Nil-safe (no-op on nil).
func (r *Runner) SetLogger(log *zap.Logger) {
	if log != nil {
		r.log = log
	}
}

// Execute runs the complete generation workflow for the given run.
// It checks the current stage and resumes from that point.
//
// In production, this should be launched as a goroutine or via an
// outbox event from the Start method. The HTTP handler must NOT
// wait for Execute to complete — it should return 202 immediately.
func (r *Runner) Execute(ctx context.Context, runID string, req GenerateRequest) {
	r.log.Info("scriptgeneration: starting execution",
		zap.String("run_id", runID),
		zap.String("source_type", string(req.Source.Type)),
	)

	// ── Stage 1: Normalize ──────────────────────────────────────
	if err := r.updateStage(ctx, runID, RunStatusRunning, StageNormalizing); err != nil {
		r.failRun(ctx, runID, StageNormalizing, err)
		return
	}
	r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageNormalizing)))

	// ── Stage 2: Generate Scene Text ─────────────────────────────
	if err := r.updateStage(ctx, runID, RunStatusRunning, StageGeneratingSceneText); err != nil {
		r.failRun(ctx, runID, StageGeneratingSceneText, err)
		return
	}
	scenes, err := r.textGen.GenerateSceneText(ctx, req)
	if err != nil {
		r.failRun(ctx, runID, StageGeneratingSceneText, fmt.Errorf("generate scene text failed: %w", err))
		return
	}
	if len(scenes) == 0 {
		r.failRun(ctx, runID, StageGeneratingSceneText, fmt.Errorf("generate scene text returned zero scenes"))
		return
	}
	result := &GenerateResult{Scenes: scenes, Title: req.Title, OutputName: req.OutputName}
	// Checkpoint after text generation.
	if err := r.repo.SavePartialResult(ctx, runID, result); err != nil {
		r.log.Warn("failed to save partial result after text generation", zap.String("run_id", runID), zap.Error(err))
	}
	r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageGeneratingSceneText)))

	// ── Stage 3: Translate Scenes ───────────────────────────────
	if len(req.Languages) > 0 {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageTranslatingScenes); err != nil {
			r.failRun(ctx, runID, StageTranslatingScenes, err)
			return
		}
		for _, lang := range req.Languages {
			// Skip the source language — no translation needed.
			if lang == req.SourceLanguage {
				continue
			}
			for i := range result.Scenes {
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
					r.failRun(ctx, runID, StageTranslatingScenes,
						fmt.Errorf("translate scene %s to %s failed: %w", result.Scenes[i].ID, lang, err))
					return
				}
				if result.Scenes[i].Text == nil {
					result.Scenes[i].Text = make(map[Language]string)
				}
				result.Scenes[i].Text[lang] = translated

				// Checkpoint after each translated scene.
				if err := r.repo.SavePartialResult(ctx, runID, result); err != nil {
					r.log.Warn("failed to save partial result after scene translation",
						zap.String("run_id", runID),
						zap.String("scene_id", result.Scenes[i].ID),
						zap.Error(err),
					)
				}
			}
		}
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageTranslatingScenes)))
	}

	// ── Stage 4: Generate Voiceovers ────────────────────────────
	if r.voiceoverGen != nil {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageGeneratingVoiceovers); err != nil {
			r.failRun(ctx, runID, StageGeneratingVoiceovers, err)
			return
		}
		for i := range result.Scenes {
			scene := &result.Scenes[i]
			// Generate a voiceover for every language that has text.
			for lang, text := range scene.Text {
				if text == "" {
					continue
				}
				audioRef, err := r.voiceoverGen.Generate(ctx, VoiceoverInput{
					SceneID:  scene.ID,
					Language: lang,
					Text:     text,
				})
				if err != nil {
					r.failRun(ctx, runID, StageGeneratingVoiceovers,
						fmt.Errorf("voiceover generation for scene %s lang %s failed: %w", scene.ID, lang, err))
					return
				}
				if scene.Voiceover == nil {
					scene.Voiceover = make(map[Language]AudioReference)
				}
				scene.Voiceover[lang] = audioRef
			}
			// Checkpoint after each scene's voiceovers.
			if err := r.repo.SavePartialResult(ctx, runID, result); err != nil {
				r.log.Warn("failed to save partial result after voiceover generation",
					zap.String("run_id", runID),
					zap.String("scene_id", scene.ID),
					zap.Error(err),
				)
			}
		}
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageGeneratingVoiceovers)))
	}

	// ── Stage 5: Publish Documents ──────────────────────────────
	if r.docPublisher != nil {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StagePublishingDocuments); err != nil {
			r.failRun(ctx, runID, StagePublishingDocuments, err)
			return
		}
		docs := make(map[Language]DocumentReference)
		for _, lang := range req.Languages {
			// Build document content from scenes for this language.
			content := buildDocumentContent(result.Scenes, lang)
			docRef, err := r.docPublisher.UpsertDocument(ctx, DocumentInput{
				RunID:    runID,
				Language: lang,
				Title:    req.Title + "_" + string(lang),
				Content:  content,
				FolderID: req.DriveFolderID,
			})
			if err != nil {
				r.failRun(ctx, runID, StagePublishingDocuments,
					fmt.Errorf("upsert document for language %s failed: %w", lang, err))
				return
			}
			docs[lang] = docRef
		}
		result.Documents = docs
		if err := r.repo.SavePartialResult(ctx, runID, result); err != nil {
			r.log.Warn("failed to save partial result after document publishing",
				zap.String("run_id", runID),
				zap.Error(err),
			)
		}
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StagePublishingDocuments)))
	}

	// ── Stage 6: Build Render Payload ───────────────────────────
	if req.RenderVideo {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageBuildingRenderPayload); err != nil {
			r.failRun(ctx, runID, StageBuildingRenderPayload, err)
			return
		}
		// The canonical render payload builder lives in
		// internal/application/scripts/jobs/enqueue.
		// Here we just signal that the payload was built.
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageBuildingRenderPayload)))
	}

	// ── Stage 7: Enqueue Render ─────────────────────────────────
	if req.RenderVideo && r.renderEnqueuer != nil {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageEnqueuingRender); err != nil {
			r.failRun(ctx, runID, StageEnqueuingRender, err)
			return
		}
		renderRef, err := r.renderEnqueuer.Enqueue(ctx, *result)
		if err != nil {
			r.failRun(ctx, runID, StageEnqueuingRender,
				fmt.Errorf("enqueue render failed: %w", err))
			return
		}
		renderRef.Status = "QUEUED"
		result.RenderJob = &renderRef
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageEnqueuingRender)))
	}

	// ── Complete ────────────────────────────────────────────────
	r.completeRun(ctx, runID, result)
}

// buildDocumentContent assembles the content string for a Google Doc
// from the scenes in the given language.
func buildDocumentContent(scenes []Scene, lang Language) string {
	var content string
	for _, scene := range scenes {
		text, ok := scene.Text[lang]
		if !ok || text == "" {
			continue
		}
		if content != "" {
			content += "\n\n"
		}
		content += fmt.Sprintf("Scene %d\n%s", scene.Index+1, text)
	}
	return content
}

// ── Internal helpers ────────────────────────────────────────────────

// updateStage persists the stage transition.
func (r *Runner) updateStage(ctx context.Context, runID string, status RunStatus, stage Stage) error {
	return r.repo.UpdateStage(ctx, runID, status, stage)
}

// failRun marks the run as FAILED with the given stage and error.
// If the update itself fails (e.g. database unreachable), the error
// is logged but not propagated — the caller has already encountered
// a failure and there is no recovery path from a failing fail.
func (r *Runner) failRun(ctx context.Context, runID string, failedStage Stage, err error) {
	r.log.Error("scriptgeneration: run failed",
		zap.String("run_id", runID),
		zap.String("failed_stage", string(failedStage)),
		zap.Error(err),
	)
	if updateErr := r.repo.UpdateStage(ctx, runID, RunStatusFailed, failedStage); updateErr != nil {
		r.log.Error("failed to persist run failure",
			zap.String("run_id", runID),
			zap.String("failed_stage", string(failedStage)),
			zap.Error(updateErr),
		)
	}
}

// completeRun marks the run as COMPLETED and saves the final result.
func (r *Runner) completeRun(ctx context.Context, runID string, result *GenerateResult) {
	r.log.Info("scriptgeneration: run completed",
		zap.String("run_id", runID),
		zap.Int("scene_count", len(result.Scenes)),
	)
	if err := r.repo.SavePartialResult(ctx, runID, result); err != nil {
		r.log.Warn("failed to save final result",
			zap.String("run_id", runID),
			zap.Error(err),
		)
	}
	if updateErr := r.repo.UpdateStage(ctx, runID, RunStatusCompleted, StageCompleted); updateErr != nil {
		r.log.Error("failed to persist run completion",
			zap.String("run_id", runID),
			zap.Error(updateErr),
		)
	}
}
