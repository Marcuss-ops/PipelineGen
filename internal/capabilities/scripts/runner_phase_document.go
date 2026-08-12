package scriptgeneration

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

func (r *Runner) runDocumentPhase(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, resumeIdx int, result *GenerateResult) bool {
	// ── Stage 5: Publish Documents ──────────────────────────────
	documentStep, startErr := r.startExecutionStep(ctx, exec, "DOCUMENT", "publication")
	if startErr != nil {
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, startErr)
		return false
	}
	// Verdetto: docs.enabled must be explicitly true. One document per
	// language is created (not one bilingual doc). The identity is
	// deterministic (run_id + language) for idempotent Upsert.
	docsEnabled, docsLangs, docsFolderID := req.ResolveDocsConfig()

	documentSkipped := stageSkipped(resumeIdx, StagePublishingDocuments) || r.docPublisher == nil || !docsEnabled || len(docsLangs) == 0
	if !documentSkipped {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StagePublishingDocuments); err != nil {
			r.failExecutionStep(ctx, exec, documentStep, err)
			r.failRunWithRetry(ctx, runID, StagePublishingDocuments, err)
			return false
		}
		docs := make(map[Language]DocumentReference)
		for _, lang := range docsLangs {
			content := buildDocumentContent(result.Scenes, lang)
			title := req.Title
			if title == "" {
				title = "Script"
			}
			docRef, err := r.docPublisher.UpsertDocument(ctx, DocumentInput{
				RunID:    runID,
				Language: lang,
				Title:    title + "_" + string(lang),
				Content:  content,
				FolderID: docsFolderID,
			})
			if err != nil {
				cause := fmt.Errorf("upsert document for language %s failed: %w", lang, err)
				r.failExecutionStep(ctx, exec, documentStep, cause)
				r.failRunWithRetry(ctx, runID, StagePublishingDocuments, cause)
				return false
			}
			docs[lang] = docRef
			if err := r.attachOutputAsset(ctx, exec, documentStep.StepID, docRef.ID, len(docs)-1); err != nil {
				r.failExecutionStep(ctx, exec, documentStep, err)
				r.failRunWithRetry(ctx, runID, StagePublishingDocuments, err)
				return false
			}
		}
		result.Documents = docs
		r.checkpoint(ctx, runID, result)
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StagePublishingDocuments)))
	}
	if documentSkipped {
		if err := r.skipExecutionStep(ctx, exec, documentStep); err != nil {
			r.failRunWithRetry(ctx, runID, StagePublishingDocuments, err)
			return false
		}
	} else if err := r.completeExecutionStep(ctx, exec, documentStep); err != nil {
		r.failExecutionStep(ctx, exec, documentStep, err)
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, err)
		return false
	}

	return true
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
		if scene.Clip != nil && strings.TrimSpace(scene.Clip.DriveLink) != "" {
			content += fmt.Sprintf("\nClip: %s", scene.Clip.DriveLink)
		}
		if vo, ok := scene.Voiceover[lang]; ok && strings.TrimSpace(vo.URL) != "" {
			content += fmt.Sprintf("\nVoiceover %s: %s", lang, vo.URL)
		}
	}
	return content
}
