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

// DocumentsService is the minimal document-service contract the
// DocumentsProcessor relies on. Production wiring injects the
// concrete *usecase.DocumentsService (whose method set satisfies
// this interface) — verified at composition time via direct pointer
// assignment.
type DocumentsService interface {
	// CreateDoc creates a Google Doc. idempotencyKey is used to
	// avoid duplicate documents on retry; forceRefresh forces an
	// update of an existing doc instead of reusing it.
	CreateDoc(ctx context.Context, title, content string, resolveFolder FolderResolver, driveFolderID, idempotencyKey string, forceRefresh bool) (link, id string)
	// UpdateDoc overwrites the content of an existing Google Doc.
	UpdateDoc(ctx context.Context, docID, title, content string) error
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

// Policy classifies document as ProcessorBestEffort (Fase 2 canonical,
// July 2026).
//
// Single canonical source of truth for the document postprocessor's
// policy — per godlike/06 SSOT one-canonical-owner-per-fact. The
// PostProcessorRegistry records this value at Register() time
// (postprocessor_registry.go:319 `policy := proc.Policy(nil)`) so
// downstream LookupPolicy / ValidateRequested / Run all consume the
// SAME value. Previously this method returned ProcessorRequired,
// which DRIFTED from the Fase 2 registry-default table
// (postprocessor_registry.go:163: `document: ProcessorBestEffort`).
// Flipping it here threads the canonical decision in ONE point.
//
// Fase 2 contract for document:
//   - Missing-registered document service (docsSvc==nil at composition)
//     becomes a warning, NOT a hard pipeline abort.
//   - Runtime Drive failure (auth/permission/quota) becomes a warning.
//   - Empty DocLink from CreateDoc() becomes a warning.
//   - Pipeline continues with the rest of the postprocessors and
//     emits a "document skipped" warning the operator sees in
//     PipelineResult.Warnings (the canonical client-facing envelope
//     per the existing voiceover_propagate_warnings precedent).
//
// The "log+continue" pattern: DocumentProcessor.Process()'s existing
// nil-docsSvc guard now returns an ErrPostprocessFailed but the
// registry's ProcessorBestEffort classification aborts the
// whole-pipeline gate and converts it into a typed warning propagated
// through PostProcessResult.Warnings + PipelineResult.Warnings.
// Failure visibility is preserved (no silent skip).
func (p *DocumentProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

// PR 5 (June 2026): signature now takes ProcessInput envelope.
//
// FASE-document-canonical (July 2026): ALWAYS calls the canonical
// renderer BuildGenerationDocumentHTML. Per godlike/06 SSOT
// one-canonical-owner-per-fact the production doc surface has a
// single canonical renderer; BuildGenerationDocumentHTML gracefully
// handles both the with-SpecScene and empty-SpecScene cases (the
// empty case skips the <h2>Scenes</h2> section via the
// `len(model.SpecScene.Scenes) > 0` guard internally). The
// `includeSpecSceneBlock` 6th parameter is set to true for production
// output — operators can visually verify the SpecScene JSON debug
// block at the bottom of every Google Doc.
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

	// Canonical single-renderer call: BuildGenerationDocumentHTML is the
	// SOLE canonical production renderer per AGENTS.md Pattern 0.
	model := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          input.Text,
		SpecScene:     input.SpecScene,
	}

	// PR-PROCESS-INPUT-ENTITIES-METADATA (July 2026): entities and
	// metadata now flow through ProcessInput from upstream
	// processors via mergePostProcessResult write-back. When nil,
	// the canonical renderer gracefully skips those sections — no
	// spurious headers, no regressions.
	//
	// Honest scope-lock (godlike/07): the write-back only takes
	// effect when the document processor runs AFTER the entities/
	// metadata processors in plan.Postprocessors execution order.
	// When it runs before, the inputs stay nil (byte-equivalent to
	// pre-PR behavior). Future reordering of the postprocessor list
	// to place document last would unlock full rendering.
	// Render the document twice: first without the final doc_id so
	// we can create the file, then again with the provenance block
	// filled in and update the file content.
	htmlContent := BuildGenerationDocumentHTML(model, docTitle, plan.Language, input.Entities, input.Metadata, true, input.Provenance)

	idempotencyKey := plan.CacheKey
	if idempotencyKey == "" {
		idempotencyKey = plan.ID
	}
	link, id := p.docsSvc.CreateDoc(ctx, docTitle, htmlContent, p.resolveFolder, plan.DriveFolderID, idempotencyKey, plan.ForceRefresh)
	if link == "" {
		return nil, fmt.Errorf("%w: document processor: Google Doc creation returned empty link", scriptpkg.ErrPostprocessFailed)
	} // Fill the provenance block with the real doc_id/doc_link and
	// rewrite the document body so it contains the complete
	// provenance metadata.
	if input.Provenance != nil {
		input.Provenance.DocID = id
		input.Provenance.DocLink = link
		input.Provenance.RequestedMode = requestedModeForPlan(plan)
		input.Provenance.UsedMode = usedModeForInput(plan, input)
		input.Provenance.FallbackUsed = input.Provenance.RequestedMode != input.Provenance.UsedMode
		htmlWithProv := BuildGenerationDocumentHTML(model, docTitle, plan.Language, input.Entities, input.Metadata, true, input.Provenance)
		if err := p.docsSvc.UpdateDoc(ctx, id, docTitle, htmlWithProv); err != nil {
			return &PostProcessResult{
				DocLink:  link,
				DocID:    id,
				Warnings: []string{fmt.Sprintf("document provenance rewrite failed: %v", err)},
			}, nil
		}
	}

	return &PostProcessResult{
		DocLink: link,
		DocID:   id,
	}, nil
}

// requestedModeForPlan returns the generation mode requested by the
// caller. Clip-aware sources request "clip_native"; everything else
// is treated as a prose generation.
func requestedModeForPlan(plan *scriptpkg.ResolvedGenerationPlan) string {
	if plan.ClipEvidence != nil && len(plan.ClipEvidence.AcceptedClipIDs) > 0 {
		return "clip_native"
	}
	switch plan.SourceKind {
	case "clips", "catalog", "search", "curate":
		return "clip_native"
	}
	return "prose"
}

// usedModeForInput returns the mode that actually produced the output.
// A clip-aware source that ends up with no scenes is considered a
// prose fallback; otherwise it is clip_native.
func usedModeForInput(plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) string {
	if requestedModeForPlan(plan) != "clip_native" {
		return "prose"
	}
	if len(input.SpecScene.Scenes) > 0 {
		return "clip_native"
	}
	return "prose"
}
