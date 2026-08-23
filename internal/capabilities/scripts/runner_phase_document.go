package scriptgeneration

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

func (r *Runner) runDocumentPhase(ctx context.Context, runID string, req GenerateRequest, routing scriptpkg.ArtifactRoutingContext, exec ExecutionContext, resumeIdx int, result *GenerateResult, skeletons map[Language]string) bool {
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
		// Idempotent restart: seed the publication maps with documents already
		// published in a prior attempt so a resume reuses them instead of
		// re-uploading (0 duplicate Docs).
		docs := make(map[Language]DocumentReference)
		if result.Documents != nil {
			for lang, ref := range result.Documents {
				docs[lang] = ref
			}
		}
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
				opts := DocumentRenderOptions{
					Title:              req.Title,
					Language:           lang,
					DefaultLanguage:    req.SourceLanguage,
					FullAudio:          documentAudioRef(result, lang),
					FinalAudio:         result.FinalAudio,
					AudioTimeline:      result.CanonicalTimeline,
					SceneSpeechTimings: result.SceneSpeechTimings,
					ClipMetadata:       clipAssetMetadataForDocument(result),
					AudioSummary:       documentAudioSummaryFor(result),
				}
				var content string
				var renderErr error
				if skeleton, ok := skeletons[lang]; ok {
					// Late-bound injection into the SceneTextReady skeleton
					// rendered by the parallel fan-out (early DocsPrepare).
					content, renderErr = r.injectDocumentLateBound(skeleton, model, opts)
				} else {
					content, renderErr = r.documentRenderer.RenderDocument(model, opts)
				}
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
		// P2: Parallelize Google Docs upload per language with bounded
		// pool (4). Each language's UpsertDocument is an independent
		// network call; running them concurrently reduces the total
		// publish time from SUM(languages) to MAX(languages).
		// Idempotent restart: already-published languages are filtered
		// before the fan-out (fast, no network calls).
		if _, publishErr := kernobs.MeasureStageReport(ctx, StageDocumentPublish, func(stageCtx context.Context) error {
			// ── Filter already-published languages ────────────────
			type docPublishJob struct {
				lang  Language
				rd    renderedDocument
				title string
			}
			var publishJobs []docPublishJob
			for _, lang := range docsLangs {
				rd := rendered[lang]
				if existing, ok := docs[lang]; ok && existing.ID != "" {
					renderers[lang] = rd.rendererID
					hashes[lang] = rd.hash
					sceneCounts[lang] = rd.sceneCount
					continue
				}
				title := req.Title
				if title == "" {
					title = "Script"
				}
				publishJobs = append(publishJobs, docPublishJob{
					lang:  lang,
					rd:    rd,
					title: title,
				})
			}

			if len(publishJobs) == 0 {
				return nil
			}

			// ── Parallel fan-out with per-language checkpoint ─────
			// Each language's UpsertDocument is an independent
			// network call. Use concurrent.Group with bounded
			// concurrency (4) via a semaphore. Each goroutine
			// checkpoints its own result ATOMICALLY so that a
			// crash after EN but before ES preserves EN on the
			// partial result (0 duplicate Docs on restart).
			const docsPublishConcurrency = 4
			group, groupCtx := concurrent.WithContext(stageCtx)
			sem := make(chan struct{}, docsPublishConcurrency)
			var publishedMu sync.Mutex

			for _, job := range publishJobs {
				group.Go(fmt.Sprintf("docs-publish-%s", job.lang), func() error {
					sem <- struct{}{}
					defer func() { <-sem }()

					var docRef DocumentReference
					if measureErr := kernobs.MeasureOperation(groupCtx, kernobs.OperationInfo{
						Stage:     StageDocumentPublish,
						Component: kernobs.ComponentGoogleDocs,
						Operation: kernobs.OperationPublish,
						Provider:  string(job.lang),
					}, func(measureCtx context.Context) error {
						var upsertErr error
						docRef, upsertErr = r.docPublisher.UpsertDocument(measureCtx, DocumentInput{
							RunID:    runID,
							Language: job.lang,
							Title:    job.title + "_" + string(job.lang),
							Content:  job.rd.content,
							FolderID: docsFolderID,
						})
						return upsertErr
					}); measureErr != nil {
						return fmt.Errorf("upsert document for language %s: %w", job.lang, measureErr)
					}

					// Per-language durable checkpoint: a crash after
					// one doc is published must preserve it on the
					// partial result so a restart reuses it.
					publishedMu.Lock()
					docs[job.lang] = docRef
					renderers[job.lang] = job.rd.rendererID
					hashes[job.lang] = job.rd.hash
					sceneCounts[job.lang] = job.rd.sceneCount
					result.Documents = docs
					result.DocumentRenderers = renderers
					result.DocumentSpecSceneSHA256 = hashes
					result.DocumentSceneCounts = sceneCounts
					r.checkpoint(groupCtx, runID, result)
					publishedMu.Unlock()

					if err := r.attachOutputAsset(groupCtx, exec, documentStep.StepID, docRef.ID, len(docs)-1); err != nil {
						return err
					}
					if err := r.recordArtifactOperation(groupCtx, exec, ArtifactOperation{
						OperationID: artifactOperationID(exec.Attempt, OperationDriveUpload, "document", string(job.lang)),
						Kind:        OperationDriveUpload,
						Language:    job.lang,
						AssetID:     docRef.ID,
						Status:      "COMPLETED",
					}); err != nil {
						return err
					}

					return nil
				})
			}

			if mapErr := group.Wait(); mapErr != nil {
				return mapErr
			}
			return nil
		}); publishErr != nil {
			cause := fmt.Errorf("document publish: %w", publishErr)
			r.failExecutionStep(ctx, exec, documentStep, cause)
			r.failRunWithRetry(ctx, runID, StagePublishingDocuments, cause)
			return false
		}
		// Maps are already projected inside the DocsPublish callback;
		// the checkpoint there is the single durable write.
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

// renderDocumentSkeletons renders the scene-text-only document skeleton for
// each docs language. It is the early DocsPrepare pass, invoked at
// SceneTextReady by the parallel fan-out so the CPU render overlaps TTS and
// NLP instead of waiting for the audio join. It returns nil when the renderer
// does not implement the early/late split or when docs are disabled, leaving
// the one-shot render path intact.
func (r *Runner) renderDocumentSkeletons(req GenerateRequest, result *GenerateResult) map[Language]string {
	splittable, ok := r.documentRenderer.(SplittableDocumentRenderer)
	if !ok || result == nil {
		return nil
	}
	docsEnabled, docsLangs, _ := req.ResolveDocsConfig()
	if !docsEnabled || len(docsLangs) == 0 {
		return nil
	}
	inputs := documentSkeletonInputForScenes(req.Title, result.Scenes, docsLangs)
	skeletons := make(map[Language]string, len(inputs))
	for lang, in := range inputs {
		skeletons[lang] = splittable.RenderDocumentSkeleton(in)
	}
	return skeletons
}

// injectDocumentLateBound fills a SceneTextReady skeleton with the late-bound
// artifacts via the splittable renderer. It is only called when a skeleton was
// rendered early, so the renderer is guaranteed to implement the split seam.
func (r *Runner) injectDocumentLateBound(skeleton string, model *scriptpkg.ModelScriptOutputV1, opts DocumentRenderOptions) (string, error) {
	splittable, ok := r.documentRenderer.(SplittableDocumentRenderer)
	if !ok {
		return "", fmt.Errorf("document renderer does not implement the early/late split")
	}
	return splittable.InjectDocumentLateBound(skeleton, model, opts), nil
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
