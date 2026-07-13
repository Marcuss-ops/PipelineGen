// Package ports — narrative_projector.go defines the canonical
// interface for projecting source material into model-facing
// evidence.
//
// Every source type (clips, search, catalog, curate, text) MUST
// produce its evidence through a single NarrativeEvidenceProjector.
// This prevents the current pattern where ClipEvidence (which
// contains infra IDs, Drive links, raw metadata) reaches the
// model directly.
//
// godlike/06 SSOT: this interface is the SINGLE canonical seam
// between source resolution and model input construction.
package ports

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// NarrativeEvidenceProjector projects resolved source material into
// model-facing evidence. It strips all infrastructure locators
// (clip_id, asset_id, Drive links, YouTube URLs, local paths, file
// hashes, raw metadata, speaker/commentator tags) and keeps only
// evidence that can change the voiceover choice.
//
// All source types converge through this interface:
//
//	SourceClips   ┐
//	SourceSearch  ├── NarrativeEvidenceProjector.Project
//	SourceCatalog ┤
//	SourceCurate  ┘
//
// The projector produces two outputs:
//   - NarrativeEvidence: model-facing (no infra IDs)
//   - BindingManifest: backend-facing (clip_id, Drive link, timestamps)
//
// Neither output is mutated after return — both are value types.
type NarrativeEvidenceProjector interface {
	// Project takes the original source text and the resolved clips,
	// and produces model-facing NarrativeEvidence alongside a
	// BindingManifest for backend binding.
	//
	// The projector MUST:
	//   - Strip all infra IDs from the evidence
	//   - Preserve narrative-safe fields (Description, VisualSummary,
	//     Transcript, DurationMs)
	//   - Map each resolved clip to a BindingSlot
	//   - Return error when the projection is impossible (no clips,
	//     invalid input)
	//
	// The projector MUST NOT:
	//   - Modify the original source text
	//   - Include clip_id, asset_id, Drive link in NarrativeEvidence
	//   - Omit clip_id from BindingManifest
	Project(
		ctx context.Context,
		originalSource scriptpkg.PlainProse,
		resolvedClips []scriptpkg.ResolvedClipSlot,
	) (scriptpkg.NarrativeEvidence, scriptpkg.BindingManifest, error)
}
