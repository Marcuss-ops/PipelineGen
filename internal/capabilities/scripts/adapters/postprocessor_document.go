package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// stageDocumentPublish is the canonical STAGE name for the Google Docs
// publish boundary owned by the documents processor. It nests under the
// processor stage recorded by the composite runner ("document") and is
// measured on the same canonical clock — never with a second ad-hoc timer.
const stageDocumentPublish kernobs.StageName = "document.publish"

// DocumentsProcessor publishes the canonical SpecScene representation to
// Google Docs when the request explicitly enables document output.
type DocumentsProcessor struct {
	service scriptports.DocumentsService
}

func NewDocumentsProcessor(service scriptports.DocumentsService) *DocumentsProcessor {
	return &DocumentsProcessor{service: service}
}

func (p *DocumentsProcessor) Name() ProcessorName { return ProcessorDocument }

func (p *DocumentsProcessor) Policy(*scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return DefaultPolicyFor(ProcessorDocument)
}

func (p *DocumentsProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if plan == nil || !plan.DocsEnabled {
		return &PostProcessResult{Changed: true}, nil
	}
	if p == nil || p.service == nil {
		return nil, fmt.Errorf("document publisher is not configured")
	}

	languages := append([]string(nil), plan.DocsLanguages...)
	if len(languages) == 0 && strings.TrimSpace(plan.Language) != "" {
		languages = []string{plan.Language}
	}
	if len(languages) == 0 {
		return nil, fmt.Errorf("document publishing requires at least one language")
	}

	// The document publisher is read-only with respect to SpecScene: it must
	// never rewrite scene state. Any upstream mismatch (e.g. single-scene
	// narrative placement) has to be resolved before this point.
	model := &scriptpkg.ModelScriptOutputV1{SchemaVersion: 1, Text: input.Text, SpecScene: input.SpecScene, WordCount: input.WordCount, ModelUsed: input.ModelUsed}
	documentTitle := resolveDocumentTitle(plan)
	var firstID, firstLink string
	var firstLanguage string
	refreshDocument := plan.ForceRefresh || specSceneHasLateBoundDocumentData(input.SpecScene)
	for _, language := range languages {
		language = strings.TrimSpace(language)
		if language == "" {
			continue
		}
		content, renderErr := scriptgen.RenderDocument(model, scriptgen.DocumentRenderOptions{
			Title:           documentTitle,
			Language:        scriptgen.Language(language),
			DefaultLanguage: scriptgen.Language(plan.Language),
		})
		if renderErr != nil {
			return nil, fmt.Errorf("render document for language %s: %w", language, renderErr)
		}
		if strings.TrimSpace(content) == "" {
			return nil, fmt.Errorf("document content is empty for language %s", language)
		}
		// google_docs.publish is the external Google Docs boundary; it nests
		// under the document.publish STAGE and shares the canonical Run clock.
		var link, id string
		if _, stageErr := kernobs.MeasureStageReport(ctx, stageDocumentPublish, func(stageCtx context.Context) error {
			return kernobs.MeasureOperation(stageCtx, kernobs.OperationInfo{
				Stage:     stageDocumentPublish,
				Component: kernobs.ComponentGoogleDocs,
				Operation: kernobs.OperationPublish,
			}, func(opCtx context.Context) error {
				var createErr error
				link, id, createErr = p.service.CreateDoc(opCtx, documentTitle+"_"+language, content, nil, plan.DocsFolderID, plan.ID+"-"+language, refreshDocument)
				return createErr
			})
		}); stageErr != nil {
			return nil, fmt.Errorf("publish document for language %s: %w", language, stageErr)
		}
		if strings.TrimSpace(link) == "" || strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("document publisher returned an empty reference for language %s", language)
		}
		if firstID == "" {
			firstID, firstLink = id, link
			firstLanguage = language
		}
	}
	if firstID == "" {
		return nil, fmt.Errorf("document publishing languages are empty")
	}
	return &PostProcessResult{
		DocID: firstID, DocLink: firstLink,
		DocumentRenderer:        scriptgen.CanonicalDocumentRendererID,
		DocumentSpecSceneSHA256: scriptgen.SpecSceneSHA256(input.SpecScene),
		DocumentSceneCount:      len(input.SpecScene.Scenes),
		DocumentLanguage:        firstLanguage,
	}, nil
}

// resolveDocumentTitle returns the caller-facing document title. It prefers the
// video metadata title when present and non-empty, otherwise it falls back to
// the plan title. A nil plan yields an empty title.
func resolveDocumentTitle(plan *scriptpkg.ResolvedGenerationPlan) string {
	if plan == nil {
		return ""
	}

	if plan.VideoMetadata != nil {
		if title := strings.TrimSpace(plan.VideoMetadata.Title); title != "" {
			return title
		}
	}

	return strings.TrimSpace(plan.Title)
}

// specSceneHasLateBoundDocumentData reports whether the SpecScene carries
// data that can arrive after the first document publish (voiceover links and
// clip subtitle links). When such data appears later, an existing document
// must be refreshed so the human surface stays current.
func specSceneHasLateBoundDocumentData(spec scriptpkg.SpecSceneOutput) bool {
	for _, scene := range spec.Scenes {
		voice := scene.Bindings.Voiceover
		if voice != nil {
			if strings.TrimSpace(voice.Link) != "" {
				return true
			}
			for _, link := range voice.Links {
				if strings.TrimSpace(link) != "" {
					return true
				}
			}
		}

		if scene.Bindings.Clip != nil && strings.TrimSpace(scene.Bindings.Clip.SubtitleLink) != "" {
			return true
		}

		for _, clip := range scene.Bindings.Clips {
			if strings.TrimSpace(clip.SubtitleLink) != "" {
				return true
			}
		}
	}
	return false
}
