package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// DocumentsProcessor publishes the canonical SpecScene representation to
// Google Docs when the request explicitly enables document output.
type DocumentsProcessor struct {
	service DocumentsService
}

func NewDocumentsProcessor(service DocumentsService) *DocumentsProcessor {
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
	documentTitle := strings.TrimSpace(plan.Title)
	if plan.VideoMetadata != nil && strings.TrimSpace(plan.VideoMetadata.Title) != "" {
		documentTitle = strings.TrimSpace(plan.VideoMetadata.Title)
	}
	var firstID, firstLink string
	refreshDocument := plan.ForceRefresh || specSceneHasLateBoundDocumentData(input.SpecScene)
	for _, language := range languages {
		language = strings.TrimSpace(language)
		if language == "" {
			continue
		}
		content := BuildSpecSceneDocumentHTML(model, SpecSceneDocumentOptions{
			Title:           documentTitle,
			Language:        language,
			DefaultLanguage: plan.Language,
		})
		if strings.TrimSpace(content) == "" {
			return nil, fmt.Errorf("document content is empty for language %s", language)
		}
		link, id, createErr := p.service.CreateDoc(ctx, documentTitle+"_"+language, content, nil, plan.DocsFolderID, plan.ID+"-"+language, refreshDocument)
		if createErr != nil {
			return nil, fmt.Errorf("publish document for language %s: %w", language, createErr)
		}
		if strings.TrimSpace(link) == "" || strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("document publisher returned an empty reference for language %s", language)
		}
		if firstID == "" {
			firstID, firstLink = id, link
		}
	}
	if firstID == "" {
		return nil, fmt.Errorf("document publishing languages are empty")
	}
	return &PostProcessResult{DocID: firstID, DocLink: firstLink}, nil
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
