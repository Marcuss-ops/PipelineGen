// Package scripts — processor_metadata.go generates YouTube-style
// metadata (title, description, tags) for the generated script.
// It delegates to the canonical PostGenUseCase (via PostGenFunc
// callback). Enabled as "metadata" in the plan's Postprocessors list.
package scripts

import (
	"context"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// MetadataProcessor generates YouTube metadata. Uses the PostGenFunc
// callback and filters for the video metadata portion of the result.
type MetadataProcessor struct {
	postGen PostGenFunc
}

// NewMetadataProcessor creates a MetadataProcessor.
func NewMetadataProcessor(postGen PostGenFunc) *MetadataProcessor {
	return &MetadataProcessor{postGen: postGen}
}

func (p *MetadataProcessor) Name() string { return "metadata" }

// PR 5 (June 2026): signature now takes ProcessInput envelope.
func (p *MetadataProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p.postGen == nil {
		return nil, fmt.Errorf("%w: metadata processor: postGen callback not configured", scriptpkg.ErrPostprocessFailed)
	}
	if input.Text == "" {
		return &PostProcessResult{}, nil
	}

	spec := legacySpecFromPlan(*plan)
	spec.GenerateMetadata = true // force ON for this processor

	_, _, videoMetadata := p.postGen(ctx, spec, input.Text)
	if len(videoMetadata) == 0 {
		return &PostProcessResult{}, nil
	}

	return &PostProcessResult{
		Metadata: videoMetadata,
	}, nil
}
