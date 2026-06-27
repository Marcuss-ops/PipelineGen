// Package scripts — processor_document.go creates a Google Doc
// from the generated script. Enabled as "document" in the plan's
// Postprocessors list.
//
// PR 3 (June 2026): uses BuildGenerationDocumentHTML to render the
// canonical typed model + accumulator-supplied entities +
// accumulator-supplied metadata into a single HTML body. Earlier
// processors (entities, metadata) accumulate their typed outputs
// into the shared accumulator; the document processor reads them
// at run time. Pre-PR-3 BuildSectionDocHTML flattener is gone.
package scripts

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// DocumentProcessor creates a Google Doc from the generated script.
// Reads accumulator.Entities + accumulator.Metadata for the
// cross-processor entities + metadata sections.
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

// Process renders BuildGenerationDocumentHTML(model, plan.Title,
// plan.Language, accumulator.Entities, accumulator.Metadata) and
// hands it to DocumentsService.CreateDoc. Returns the typed
// DocumentArtifact on success.
//
// PR 3 (June 2026): the canonical typed model is the source of
// truth for scenes + bindings. Pre-PR-3 inputs (flat section
// titles + contents, BuildSectionDocHTML) are gone.
func (p *DocumentProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	model *scriptpkg.ModelScriptOutputV1,
	accumulator *PostProcessArtifact,
) (*PostProcessArtifact, error) {
	if p.docsSvc == nil {
		return nil, fmt.Errorf("%w: document processor: DocumentsService not configured", scriptpkg.ErrPostprocessFailed)
	}
	if model == nil || plan == nil {
		return &PostProcessArtifact{}, nil
	}

	docTitle := strings.TrimSpace(plan.Title)
	if docTitle == "" {
		docTitle = "Generated Script"
	}

	// Look up entities + metadata from the shared accumulator.
	// PR 3 conversion note: accumulator.Metadata carries the
	// scripts.VideoMetadata shape (live in this package because
	// the legacy VideoMetadata was defined here). The domain
	// VideoMetadata (scriptpkg.VideoMetadata) is what
	// BuildGenerationDocumentHTML expects. The conversion loop
	// below is a structural copy — same Go shape, different
	// package identity. A PR 7+ cleanup migrates scripts.VideoMetadata
	// to scriptpkg.VideoMetadata exclusively and removes this
	// copy.
	var entities *scriptpkg.EntityResult
	var metadata []scriptpkg.VideoMetadata
	if accumulator != nil {
		entities = accumulator.Entities
		if accMetadata := accumulator.Metadata; len(accMetadata) > 0 {
			metadata = make([]scriptpkg.VideoMetadata, len(accMetadata))
			for i := range accMetadata {
				metadata[i] = scriptpkg.VideoMetadata{
					Language:    accMetadata[i].Language,
					Title:       accMetadata[i].Title,
					Description: accMetadata[i].Description,
					Tags:        accMetadata[i].Tags,
				}
			}
		}
	}

	htmlContent := BuildGenerationDocumentHTML(model, docTitle, plan.Language, entities, metadata)

	link, id := p.docsSvc.CreateDoc(ctx, docTitle, htmlContent, p.resolveFolder, plan.DriveFolderID)
	if link == "" {
		return nil, fmt.Errorf("%w: document processor: Google Doc creation returned empty link", scriptpkg.ErrPostprocessFailed)
	}

	return &PostProcessArtifact{
		Document: &scriptpkg.DocumentArtifact{
			DocLink: link,
			DocID:   id,
			Status:  "completed",
		},
	}, nil
}
