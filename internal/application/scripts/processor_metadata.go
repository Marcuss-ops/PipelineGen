// Package scripts — processor_metadata.go generates YouTube-style
// metadata (title, description, tags) for the generated script.
// Delegates to the canonical PostGenUseCase (via PostGenFunc
// callback). Enabled as "metadata" in the plan's Postprocessors list.
//
// PR 3 (June 2026): the typed VideoMetadata slice flows directly
// into PostProcessArtifact.Metadata. PostGenFunc is now declared
// in postprocessor_registry.go (single canonical location).
package scripts

import (
	"context"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// MetadataProcessor generates YouTube metadata. Reads model.Text
// and runs the postgen canonical callback for the video-metadata
// phase.
type MetadataProcessor struct {
	postGen PostGenFunc
}

// NewMetadataProcessor creates a MetadataProcessor.
// postGen may be nil — the processor returns an empty
// Metadata slice on nil (mirrors pre-PR-3 behaviour).
func NewMetadataProcessor(postGen PostGenFunc) *MetadataProcessor {
	return &MetadataProcessor{postGen: postGen}
}

func (p *MetadataProcessor) Name() string { return "metadata" }

// Process reads model.Text and runs the video-metadata extractor
// over it. Returns a *PostProcessArtifact{Metadata: ...} or
// empty when the extractor is nil or the text is empty.
func (p *MetadataProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	model *scriptpkg.ModelScriptOutputV1,
	_ *PostProcessArtifact,
) (*PostProcessArtifact, error) {
	if model == nil || plan == nil {
		return &PostProcessArtifact{}, nil
	}
	if model.Text == "" {
		return &PostProcessArtifact{}, nil
	}
	if p.postGen == nil {
		// PR 3 contract: empty PostProcessArtifact when the
		// metadata extractor is unwired. Mirrors pre-PR-3
		// behaviour where VideoMetadata was simply empty.
		return &PostProcessArtifact{}, nil
	}

	spec := legacySpecFromPlan(*plan)
	spec.GenerateMetadata = true // force ON for this processor

	_, videoMetadata, err := p.postGen(ctx, spec, model.Text)
	if err != nil {
		return nil, fmt.Errorf("%w: metadata processor: postGen callback failed: %w", scriptpkg.ErrPostprocessFailed, err)
	}
	if len(videoMetadata) == 0 {
		return &PostProcessArtifact{}, nil
	}

	return &PostProcessArtifact{Metadata: videoMetadata}, nil
}
