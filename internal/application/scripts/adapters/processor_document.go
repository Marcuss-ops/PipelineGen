// Package scripts — processor_document.go creates a Google Doc
// from the generated script. Enabled as "document" in the plan's
// Postprocessors list.
package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── FolderResolver + DocumentsService contracts (mirror) ────────────────
// adapters/ owns its own FolderResolver alias + DocumentsService
// interface to avoid an adapters→usecase import cycle (usecase
// already imports adapters for PostProcessorRegistry + ProcessInput).
// Both names resolve to the same Go types as usecase.FolderResolver +
// the usecase.DocumentsService concrete (which satisfies the
// DocumentsService interface).

// FolderResolver translates a logical folder ID into the canonical
// Drive folder to use; the resolver may return the input verbatim
// when no rewriting is needed.
type FolderResolver = func(ctx context.Context, folderID string, fallback string) (string, error)

// DocumentsService is the minimal CreateDoc signature the
// DocumentsProcessor relies on. Production wiring injects the
// concrete *usecase.DocumentsService (whose CreateDoc method set
// satisfies this interface) — verified at composition time via
// direct pointer assignment.
type DocumentsService interface {
	CreateDoc(ctx context.Context, title, content string, resolveFolder FolderResolver, driveFolderID string) (link, id string)
}

// DocumentProcessor creates a Google Doc from the generated script.
type DocumentProcessor struct {
	docsSvc       DocumentsService
	resolveFolder FolderResolver
}

// NewDocumentProcessor creates a DocumentProcessor.
// docsSvc must satisfy DocumentsService (production: *usecase.DocumentsService).
// resolveFolder may be nil (default folder used).
func NewDocumentProcessor(docsSvc DocumentsService, resolveFolder FolderResolver) *DocumentProcessor {
	return &DocumentProcessor{
		docsSvc:       docsSvc,
		resolveFolder: resolveFolder,
	}
}

func (p *DocumentProcessor) Name() string { return "document" }

// Policy classifies document as ProcessorRequired: a missing-registered
// document service or a runtime failure or empty DocLink output is a
// hard failure.
func (p *DocumentProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorRequired
}

// PR 5 (June 2026): signature now takes ProcessInput envelope.
func (p *DocumentProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p.docsSvc == nil {
		return nil, fmt.Errorf("%w: document processor: DocumentsService not configured", scriptpkg.ErrPostprocessFailed)
	}
	if input.Text == "" && len(input.SpecScene.Scenes) == 0 {
		return &PostProcessResult{}, nil
	}

	docTitle := strings.TrimSpace(plan.Title)
	if docTitle == "" {
		docTitle = "Generated Script"
	}

	htmlContent := ""
	if len(input.SpecScene.Scenes) > 0 {
		model := &scriptpkg.ModelScriptOutputV1{
			SchemaVersion: 1,
			Text:          input.Text,
			SpecScene:     input.SpecScene,
		}
		htmlContent = BuildClipSpecSceneDocumentHTML(model, docTitle, input.SourceTrace)
	} else {
		htmlContent = BuildSectionDocHTML(docTitle, []string{""}, []string{input.Text}, true, plan.Language)
	}

	link, id := p.docsSvc.CreateDoc(ctx, docTitle, htmlContent, p.resolveFolder, plan.DriveFolderID)
	if link == "" {
		return nil, fmt.Errorf("%w: document processor: Google Doc creation returned empty link", scriptpkg.ErrPostprocessFailed)
	}

	return &PostProcessResult{
		DocLink: link,
		DocID:   id,
	}, nil
}
