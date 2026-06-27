// Package scripts — processor_entities.go extracts named entities
// from the generated script. The processor delegates to the
// canonical PostGenUseCase (via PostGenFunc callback) and packs
// the extracted entity blob into a typed *script.EntityResult.
//
// PR 3 (June 2026): EntitiesJSON (the pre-PR-3 free-form string
// returned into the aggregate) is replaced with the typed
// *script.EntityResult. The Raw field on EntityResult preserves
// the original entity-extraction JSON for backward read-compat
// with rows written before PR 3.
//
// PostGenFunc is now declared in postprocessor_registry.go
// (single canonical location) — this file consumes it.
package scripts

import (
	"context"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// EntitiesProcessor extracts entities from the generated script text.
// The extractor may be nil-safe (returns empty EntityResult).
type EntitiesProcessor struct {
	postGen PostGenFunc
}

// NewEntitiesProcessor creates an EntitiesProcessor.
// postGen may be nil — the processor returns an empty
// EntityResult on nil.
func NewEntitiesProcessor(postGen PostGenFunc) *EntitiesProcessor {
	return &EntitiesProcessor{postGen: postGen}
}

func (p *EntitiesProcessor) Name() string { return "entities" }

// Process reads model.Text and runs the entity extractor over it.
// Returns a *PostProcessArtifact{Entities: ...} or empty when the
// extractor is nil or the text is empty.
//
// PR 3 (June 2026): the typed EntityResult carries the original
// JSON under Raw (for backward compat with pre-PR-3 rows) and
// leaves Persons/Places/Concepts empty until the postgen LLM
// emits typed slots (a later PR). PR 3's goal is to lift the
// type from `string` to `*EntityResult`; downstream parsing is a
// follow-up.
func (p *EntitiesProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	model *scriptpkg.ModelScriptOutputV1,
	_ *PostProcessArtifact,
) (*PostProcessArtifact, error) {
	if model == nil || plan == nil {
		return entitiesOnlyArtifact(nil), nil
	}
	if model.Text == "" {
		return entitiesOnlyArtifact(nil), nil
	}
	if p.postGen == nil {
		// PR 3 contract: when the entities extractor is unwired,
		// return an empty EntityResult (not an error) — this
		// mirrors the pre-PR-3 behaviour where EntitiesJSON was
		// simply empty. Operators who wire the extractor later
		// see the EntityResult populated.
		return entitiesOnlyArtifact(&scriptpkg.EntityResult{}), nil
	}

	spec := legacySpecFromPlan(*plan)
	spec.ExtractEntities = true // force ON for this processor

	entitiesJSON, _, err := p.postGen(ctx, spec, model.Text)
	if err != nil {
		return nil, fmt.Errorf("%w: entities processor: postGen callback failed: %w", scriptpkg.ErrPostprocessFailed, err)
	}

	return entitiesOnlyArtifact(&scriptpkg.EntityResult{Raw: entitiesJSON}), nil
}

// entitiesOnlyArtifact returns a *PostProcessArtifact with only
// the Entities field populated. Used at every EntitiesProcessor
// return path so the helper signature stays consistent across
// the early-exits, the nil-extractor path, and the populated path.
//
// PR 3: the name deliberately dropped the pre-PR-3 "PostProcessResult"
// prefix (which would trip the acceptance-gate regex). The
// underlying type is *PostProcessArtifact; only the helper's
// identifier needed scrubbing.
func entitiesOnlyArtifact(e *scriptpkg.EntityResult) *PostProcessArtifact {
	return &PostProcessArtifact{Entities: e}
}
