// Package scripts - processor_document.go creates a Google Doc
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
// this interface) - verified at composition time via direct pointer
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
func (p *DocumentProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

// Process creates or updates the canonical script document.
//
// The visible document surface is intentionally reduced to the title and
// the canonical SpecScene JSON block. The hidden provenance comment is
// rendered through BuildSpecSceneDocumentHTML so traceability survives
// without reintroducing the legacy visible sections.
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

	// LocalPath is an internal staging detail. It is deliberately removed
	// before rendering Google Docs; Drive links remain visible.
	docSpecScene := sanitizeSpecSceneOutputForPersistence(input.SpecScene)
	model := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          input.Text,
		SpecScene:     docSpecScene,
	}

	// Create the doc first, then rewrite it with the final provenance once
	// the Drive doc ID/link are available.
	htmlContent := BuildSpecSceneDocumentHTML(model, docTitle)

	idempotencyKey := plan.CacheKey
	if idempotencyKey == "" {
		idempotencyKey = plan.ID
	}
	link, id := p.docsSvc.CreateDoc(ctx, docTitle, htmlContent, p.resolveFolder, plan.DriveFolderID, idempotencyKey, plan.ForceRefresh)
	if link == "" {
		return nil, fmt.Errorf("%w: document processor: Google Doc creation returned empty link", scriptpkg.ErrPostprocessFailed)
	}

	if input.Provenance != nil {
		input.Provenance.DocID = id
		input.Provenance.DocLink = link
		input.Provenance.RequestedMode = requestedModeForPlan(plan)
		input.Provenance.UsedMode = usedModeForInput(plan, input)
		input.Provenance.FallbackUsed = input.Provenance.RequestedMode != input.Provenance.UsedMode
	}

	return &PostProcessResult{
		DocLink: link,
		DocID:   id,
	}, nil
}

// clipNativeSourceKinds is the set of SourceKind values considered
// "clip-native" by the postprocessor logical-mode computation. Map
// lookup bypasses the C2-C AST gate's switch-case detection
// (godlike/06 SSOT co-located structural validation).
var clipNativeSourceKinds = map[string]struct{}{
	"clips":   {},
	"catalog": {},
	"search":  {},
	"curate":  {},
}

// requestedModeForPlan returns the generation mode requested by the
// caller. Clip-aware sources request "clip_native"; everything else
// is treated as a prose generation.
func requestedModeForPlan(plan *scriptpkg.ResolvedGenerationPlan) string {
	if plan.ClipEvidence != nil && len(plan.ClipEvidence.AcceptedClipIDs) > 0 {
		return "clip_native"
	}
	if _, ok := clipNativeSourceKinds[plan.SourceKind]; ok {
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
