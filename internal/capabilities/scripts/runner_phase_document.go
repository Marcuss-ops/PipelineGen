package scriptgeneration

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

func (r *Runner) runDocumentPhase(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, resumeIdx int, result *GenerateResult) bool {
	// ── Publish Documents after the final audio/render payload ───
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
		if r.documentRenderer == nil {
			cause := fmt.Errorf("canonical document renderer is not configured")
			r.failExecutionStep(ctx, exec, documentStep, cause)
			r.failRunWithRetry(ctx, runID, StagePublishingDocuments, cause)
			return false
		}
		docs := make(map[Language]DocumentReference)
		renderers := make(map[Language]string)
		hashes := make(map[Language]string)
		sceneCounts := make(map[Language]int)
		for _, lang := range docsLangs {
			model := modelScriptOutputForDocument(result, lang)
			content, renderErr := r.documentRenderer.RenderDocument(model, DocumentRenderOptions{
				Title:           req.Title,
				Language:        lang,
				DefaultLanguage: req.SourceLanguage,
				FullAudio:       documentAudioRef(result, lang),
				FinalAudio:      result.FinalAudio,
				AudioTimeline:   result.CanonicalTimeline,
				Overlay:         documentOverlayRef(result),
			})
			if renderErr != nil {
				cause := fmt.Errorf("render document for language %s failed: %w", lang, renderErr)
				r.failExecutionStep(ctx, exec, documentStep, cause)
				r.failRunWithRetry(ctx, runID, StagePublishingDocuments, cause)
				return false
			}
			rendererID := "unknown"
			if identified, ok := r.documentRenderer.(IdentifiedDocumentRenderer); ok {
				rendererID = identified.DocumentRendererID()
			}
			renderers[lang] = rendererID
			hashes[lang] = documentSpecSceneSHA256(model)
			sceneCounts[lang] = len(model.SpecScene.Scenes)
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
		result.DocumentRenderers = renderers
		result.DocumentSpecSceneSHA256 = hashes
		result.DocumentSceneCounts = sceneCounts
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

func documentAudioRef(result *GenerateResult, language Language) *DocumentAudioRef {
	if result == nil || result.FinalAudio == nil {
		return nil
	}
	ref := result.FinalAudio
	return &DocumentAudioRef{
		AssetID: ref.AssetID, Language: string(language), DriveLink: ref.DriveLink,
		DurationMS: ref.DurationMS, SHA256: ref.FinalAudioSHA256,
	}
}

// documentOverlayRef projects the completed render artifact into the
// published-only DocumentOverlayRef the document consumes. It never leaks a
// local path: only the artifact URL, its integrity hash and the copy-only
// certification survive.
func documentOverlayRef(result *GenerateResult) *DocumentOverlayRef {
	if result == nil || result.RenderJob == nil || result.RenderJob.Artifact == nil {
		return nil
	}
	artifact := result.RenderJob.Artifact
	return &DocumentOverlayRef{
		ArtifactID:   artifact.ID,
		JobID:        result.RenderJob.JobID,
		URL:          artifact.URL,
		SHA256:       artifact.SHA256,
		DurationUS:   artifact.DurationUS,
		ProfileID:    artifact.ProfileID,
		CopyEligible: artifact.CopyEligible,
	}
}
