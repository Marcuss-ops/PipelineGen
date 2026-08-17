package scriptgeneration

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func (r *Runner) runDocumentPhase(ctx context.Context, runID string, req GenerateRequest, routing kernelscript.ArtifactRoutingContext, exec ExecutionContext, resumeIdx int, result *GenerateResult) bool {
	// ── Publish Documents after the final audio/render payload ───
	documentStep, startErr := r.startExecutionStep(ctx, exec, "DOCUMENT", "publication")
	if startErr != nil {
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, startErr)
		return false
	}
	// Verdetto: docs.enabled must be explicitly true. One document per
	// language is created (not one bilingual doc). The identity is
	// deterministic (run_id + language) for idempotent Upsert.
	// Folder routing comes from the canonical routing context resolved once at
	// generation start; only the enabled/languages toggle is request-local.
	docsEnabled, docsLangs, _ := req.ResolveDocsConfig()
	docsFolderID := routing.DocsFolderID

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

		// ── DocsPrepare ──────────────────────────────────────────────
		// Render every language's document HTML first. The render is CPU work
		// that is fully independent of the Google Docs network publish, so it
		// is measured as its own document.prepare stage. It still runs after
		// the audio join because the document projects late-bound artifacts
		// (voiceover links, entities, timing, final audio) — the render
		// itself never blocks NLP/TTS/audio; those phases complete first.
		type renderedDocument struct {
			content    string
			rendererID string
			hash       string
			sceneCount int
		}
		rendered := make(map[Language]renderedDocument, len(docsLangs))
		if _, prepareErr := kernobs.MeasureStageReport(ctx, StageDocumentPrepare, func(stageCtx context.Context) error {
			for _, lang := range docsLangs {
				model := modelScriptOutputForDocument(result, lang)
				content, renderErr := r.documentRenderer.RenderDocument(model, DocumentRenderOptions{
					Title:              req.Title,
					Language:           lang,
					DefaultLanguage:    req.SourceLanguage,
					FullAudio:          documentAudioRef(result, lang),
					FinalAudio:         result.FinalAudio,
					AudioTimeline:      result.CanonicalTimeline,
					SceneSpeechTimings: result.SceneSpeechTimings,
					ClipMetadata:       clipAssetMetadataForDocument(result),
					AudioSummary:       documentAudioSummaryFor(result),
				})
				if renderErr != nil {
					return fmt.Errorf("render document for language %s: %w", lang, renderErr)
				}
				rendererID := "unknown"
				if identified, ok := r.documentRenderer.(IdentifiedDocumentRenderer); ok {
					rendererID = identified.DocumentRendererID()
				}
				rendered[lang] = renderedDocument{
					content:    content,
					rendererID: rendererID,
					hash:       documentSpecSceneSHA256(model),
					sceneCount: len(model.SpecScene.Scenes),
				}
			}
			return nil
		}); prepareErr != nil {
			cause := fmt.Errorf("document prepare: %w", prepareErr)
			r.failExecutionStep(ctx, exec, documentStep, cause)
			r.failRunWithRetry(ctx, runID, StagePublishingDocuments, cause)
			return false
		}

		// ── DocsPublish ─────────────────────────────────────────────
		// Join the final required artifacts and upload each rendered document.
		// This is the only network boundary in the docs branch and the last
		// join in the run: it uploads the already-prepared HTML to Google Docs.
		if _, publishErr := kernobs.MeasureStageReport(ctx, StageDocumentPublish, func(stageCtx context.Context) error {
			for _, lang := range docsLangs {
				rd := rendered[lang]
				title := req.Title
				if title == "" {
					title = "Script"
				}
				var docRef DocumentReference
				if opErr := kernobs.MeasureOperation(stageCtx, kernobs.OperationInfo{
					Stage:     StageDocumentPublish,
					Component: kernobs.ComponentGoogleDocs,
					Operation: kernobs.OperationPublish,
				}, func(opCtx context.Context) error {
					var upsertErr error
					docRef, upsertErr = r.docPublisher.UpsertDocument(opCtx, DocumentInput{
						RunID:    runID,
						Language: lang,
						Title:    title + "_" + string(lang),
						Content:  rd.content,
						FolderID: docsFolderID,
					})
					return upsertErr
				}); opErr != nil {
					return fmt.Errorf("upsert document for language %s: %w", lang, opErr)
				}
				docs[lang] = docRef
				renderers[lang] = rd.rendererID
				hashes[lang] = rd.hash
				sceneCounts[lang] = rd.sceneCount
				if err := r.attachOutputAsset(stageCtx, exec, documentStep.StepID, docRef.ID, len(docs)-1); err != nil {
					return err
				}
			}
			return nil
		}); publishErr != nil {
			cause := fmt.Errorf("document publish: %w", publishErr)
			r.failExecutionStep(ctx, exec, documentStep, cause)
			r.failRunWithRetry(ctx, runID, StagePublishingDocuments, cause)
			return false
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
