// Package scripts — processor_document.go creates a Google Doc
// from the generated script. Enabled as "document" in the plan's
// Postprocessors list.
package scripts

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// DocumentProcessor creates a Google Doc from the generated script.
type DocumentProcessor struct {
	docsSvc       *DocumentsService
	resolveFolder FolderResolver
}

// NewDocumentProcessor creates a DocumentProcessor.
// docsSvc must be non-nil; resolveFolder may be nil (default folder used).
func NewDocumentProcessor(docsSvc *DocumentsService, resolveFolder FolderResolver) *DocumentProcessor {
	return &DocumentProcessor{
		docsSvc:       docsSvc,
		resolveFolder: resolveFolder,
	}
}

func (p *DocumentProcessor) Name() string { return "document" }

// PR 5 (June 2026): signature now takes ProcessInput envelope.
func (p *DocumentProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p.docsSvc == nil {
		return nil, fmt.Errorf("%w: document processor: DocumentsService not configured", scriptpkg.ErrPostprocessFailed)
	}
	if input.Text == "" {
		return &PostProcessResult{}, nil
	}

	docTitle := strings.TrimSpace(plan.Title)
	if docTitle == "" {
		docTitle = "Generated Script"
	}

	htmlContent := BuildSectionDocHTML(docTitle, []string{""}, []string{input.Text}, true, plan.Language)

	link, id := p.docsSvc.CreateDoc(ctx, docTitle, htmlContent, p.resolveFolder, plan.DriveFolderID)
	if link == "" {
		return nil, fmt.Errorf("%w: document processor: Google Doc creation returned empty link", scriptpkg.ErrPostprocessFailed)
	}

	return &PostProcessResult{
		DocLink: link,
		DocID:   id,
	}, nil
}
