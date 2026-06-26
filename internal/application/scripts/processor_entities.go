// Package scripts — processor_entities.go extracts named entities
// and insights from the generated script. It delegates to the
// canonical PostGenUseCase (via PostGenFunc callback).
package scripts

import (
	"context"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// EntitiesProcessor extracts entities and insights from the
// generated script text. Enabled as "entities" in the plan's
// Postprocessors list.
type EntitiesProcessor struct {
	// postGen is the PostGenFunc callback wired from PostGenUseCase.
	// When nil, Process returns ErrPostprocessFailed.
	postGen PostGenFunc
}

// NewEntitiesProcessor creates an EntitiesProcessor.
func NewEntitiesProcessor(postGen PostGenFunc) *EntitiesProcessor {
	return &EntitiesProcessor{postGen: postGen}
}

func (p *EntitiesProcessor) Name() string { return "entities" }

func (p *EntitiesProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, script string) (*PostProcessResult, error) {
	if p.postGen == nil {
		return nil, fmt.Errorf("%w: entities processor: postGen callback not configured", scriptpkg.ErrPostprocessFailed)
	}
	if script == "" {
		return &PostProcessResult{}, nil
	}

	// Build a legacy GenerationSpec for the callback.
	spec := legacySpecFromPlan(*plan)
	spec.ExtractEntities = true // force ON for this processor

	entitiesJSON, _, _ := p.postGen(ctx, spec, script)
	if entitiesJSON == "" {
		return &PostProcessResult{}, nil
	}
	return &PostProcessResult{
		EntitiesJSON: entitiesJSON,
	}, nil
}
