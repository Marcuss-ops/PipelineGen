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

func (p *DocumentsProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
) (*PostProcessResult, error) {
	if p == nil || p.service == nil {
		return nil, fmt.Errorf("document publisher is not configured")
	}
	if plan == nil || !plan.DocsEnabled {
		return &PostProcessResult{Changed: true}, nil
	}

	languages := append([]string(nil), plan.DocsLanguages...)
	if len(languages) == 0 && strings.TrimSpace(plan.Language) != "" {
		languages = []string{plan.Language}
	}
	if len(languages) == 0 {
		return nil, fmt.Errorf("document publishing requires at least one language")
	}

	model := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          input.Text,
		SpecScene:     input.SpecScene,
		WordCount:     input.WordCount,
		ModelUsed:     input.ModelUsed,
	}
	content := BuildSpecSceneDocumentHTML(model, plan.Title, input.Provenance)
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("document content is empty")
	}

	var firstID, firstLink string
	for _, language := range languages {
		language = strings.TrimSpace(language)
		if language == "" {
			continue
		}
		link, id := p.service.CreateDoc(
			ctx,
			plan.Title+"_"+language,
			content,
			nil,
			plan.DocsFolderID,
			plan.ID+"-"+language,
			plan.ForceRefresh,
		)
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
