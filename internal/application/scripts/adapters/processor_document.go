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

func (p *DocumentProcessor) Name() ProcessorName { return ProcessorDocument }

// Policy classifies document as ProcessorRequired: a missing-registered
// document service or a runtime failure or empty DocLink output is a
// hard failure.
func (p *DocumentProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorRequired
}

// PR 5 (June 2026): signature now takes ProcessInput envelope.
//
// FASE-document-canonical (July 2026): ALWAYS calls the canonical
// renderer BuildGenerationDocumentHTML (was: dual-branch
// BuildClipSpecSceneDocumentHTML vs BuildSectionDocHTML). Per
// godlike/06 SSOT one-canonical-owner-per-fact the production doc
// surface has a single canonical renderer; BuildGenerationDocumentHTML
// gracefully handles both the with-SpecScene and empty-SpecScene
// cases (the empty case skips the <h2>Scenes</h2> section via the
// `len(model.SpecScene.Scenes) > 0` guard internally). The optional
// `includeSpecSceneBlock` 6th parameter is set to false for production
// pristine output — the SpecScene JSON textual dump is opt-in
// (godlike/07 minimal-blast-radius; operators wanting the debug
// block can call the canonical renderer with includeSpecSceneBlock
// =true directly).
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

	// Canonical single-renderer call (was: BuildClipSpecSceneDocumentHTML
	// when len(SpecScene.Scenes) > 0, BuildSectionDocHTML otherwise).
	model := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          input.Text,
		SpecScene:     input.SpecScene,
	}

	// ── Honest-limitation audit-pin (godlike/07 no-fake-availability) ──
	// BuildGenerationDocumentHTML accepts entities (scriptpkg.EntityResult)
	// and metadata ([]scriptpkg.VideoMetadata) explicitly — but we pass
	// `nil, nil` here because adapters.ProcessInput (the canonical envelope
	// wired from GenerateOneUseCase through the registry) doesn't carry
	// them today. The pre-FASE-document-canonical dual-branch renderer
	// (`BuildClipSpecSceneDocumentHTML` + `BuildSectionDocHTML`) ALSO
	// didn't propagate them, so this refactor doesn't regress behaviour
	// (the title, prose, scenes, bindings, and per-scene <a href> drive
	// links render in BOTH before/after paths); it just doesn't YET
	// expose them.
	//
	// Forward-pointer: PR-PROCESS-INPUT-ENTITIES-METADATA (deferred; the
	// canonical migration is to extend adapters.ProcessInput with two new
	// optional fields — `Entities *scriptpkg.EntityResult` (omitempty,
	// populated by the entity_parser adapter) and `Metadata
	// []scriptpkg.VideoMetadata` (omitempty, populated by the
	// metadata_generator adapter) — and thread them from
	// GenerateOneUseCase's postResult.Entities + postResult.VideoMetadata
	// into the canonical call site here. Once that lands the `nil, nil`
	// literal below becomes `entities, metadata` and the canonical
	// renderer auto-surfaces the `<h2>Entities</h2>` + `<h2>Video
	// Metadata</h2>` sections without further changes).
	htmlContent := BuildGenerationDocumentHTML(model, docTitle, plan.Language, nil, nil, false)

	link, id := p.docsSvc.CreateDoc(ctx, docTitle, htmlContent, p.resolveFolder, plan.DriveFolderID)
	if link == "" {
		return nil, fmt.Errorf("%w: document processor: Google Doc creation returned empty link", scriptpkg.ErrPostprocessFailed)
	}

	return &PostProcessResult{
		DocLink: link,
		DocID:   id,
	}, nil
}
