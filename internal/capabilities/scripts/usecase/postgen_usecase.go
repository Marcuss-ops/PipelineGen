// Package scripts — postgen_usecase.go is the post-generation phase
// for the unified clip-source script generation job (entities
// extraction + insights + multi-lingual video metadata).
//
// BuildMetadataLanguages and GenerateVideoMetadata are in the same
// package (metadata.go). The use case calls them directly instead of
// receiving them as opaque function-port deps from the API layer. LanguageBuilderFunc and MetadataGenFunc are removed;
// the constructor is simplified accordingly.
//
// The use case owns:
//   - the parallel entities + insights phase (ExtractEntities flag)
//   - the parallel multi-language video metadata phase (GenerateMetadata flag)
//   - nil-safe short-circuit when both flags are false OR the script is empty
//
// The use case does NOT own:
//   - spec decode (caller responsibility)
//   - HTTP transport shape (handler responsibility)
//   - documents/scenes pipeline phases (Pipeline + DocumentsUseCase /
//     SceneBuilderUseCase respectively)
package usecase

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/dto"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"

	"go.uber.org/zap"
)

// ── Result type ─────────────────────────────────────────────────────────────

// PostGenResult encapsulates the outputs of the post-generation phase.
// PR 8 (June 2026): the in-package VideoMetadata alias is gone —
// the canonical shape is scriptpkg.VideoMetadata
// (internal/kernel/script/generation_result.go).
type PostGenResult struct {
	EntitiesJSON  string
	Insights      ScriptInsights
	VideoMetadata []scriptpkg.VideoMetadata
}

// InsightBuilder is the narrow port the use case consumes to convert
// entity-analysis JSON into the rich ScriptInsights struct.
type InsightBuilder interface {
	Build(ctx context.Context, title, script, entitiesJSON string) ScriptInsights
}

// ── Use case ────────────────────────────────────────────────────────────────

// PostGenUseCase orchestrates the post-generation extraction + translation
// phases. Calls BuildMetadataLanguages and GenerateVideoMetadata (metadata.go)
// directly — no longer receives them as opaque function-port deps.
type PostGenUseCase struct {
	extractor      EntityScriptExtractor
	insightBuilder InsightBuilder
	generator      dto.MetadataGenerator
	metadataModel  string
	log            *zap.Logger
}

// NewPostGenUseCase constructs the use case. All deps are nil-safe.
func NewPostGenUseCase(
	extractor EntityScriptExtractor,
	insightBuilder InsightBuilder,
	generator dto.MetadataGenerator,
	metadataModel string,
	log *zap.Logger,
) *PostGenUseCase {
	return &PostGenUseCase{
		extractor:      extractor,
		insightBuilder: insightBuilder,
		generator:      generator,
		metadataModel:  metadataModel,
		log:            log,
	}
}

// Run executes the parallel extraction + translation phases.
// Returns empty PostGenResult (no error) when any of:
//
//   - the use case was not constructed (nil receiver — early-exit)
//   - the script is empty (mirrors the original nil-pathResult early-exit)
//   - payload is nil OR neither flag (ExtractEntities nor
//     GenerateMetadata) is true
//
// Otherwise both phases run concurrently via concurrent.WithContext
// — first-error-wins + panic recovery are visible to the operator via
// the structured logger the use case was built with. Phase-level
// failures are logged WARN and DO NOT propagate (preserves the
// original handler's "best-effort, continue on failure" semantics).
//
// Errors from the extractor are NOT returned; they are logged + the
// entities block is left empty. Errors from the metadata generator
// are similarly handled inside GenerateVideoMetadata (a nil generator
// is gracefully tolerated — translations return empty strings, the
// function returns nil). Matches the original handlePostGeneration
// call site which had zero nil-checks around the generator pointer.
//
// The only error this function returns is the typed
// concurrent.WithContext panic-recovery error if a phase panicked;
// it is logged + ignored upstream by the original handler's caller
// pattern. For correctness, Run returns nil error unless a panic
// occurred, mirroring the original behaviour.
func (u *PostGenUseCase) Run(ctx context.Context, payload *scriptpkg.GenerationSpec, script string) (PostGenResult, error) {
	var res PostGenResult
	if u == nil || script == "" {
		return res, nil
	}
	if payload != nil && !payload.ExtractEntities && !payload.GenerateMetadata {
		return res, nil
	}

	group, groupCtx := concurrent.WithContext(ctx)

	if payload != nil && payload.ExtractEntities {
		group.Go("entities-and-insights", func() error {
			ents, err := ExtractScriptEntities(groupCtx, u.extractor, script, u.metadataModel)
			if err != nil {
				if u.log != nil {
					u.log.Warn("failed to extract entities", zap.Error(err))
				}
			}
			res.EntitiesJSON = ents
			if u.insightBuilder != nil {
				res.Insights = u.insightBuilder.Build(groupCtx, payload.Title, script, ents)
			}
			return nil
		})
	}

	if u.generator != nil && payload != nil && payload.GenerateMetadata {
		group.Go("video-metadata", func() error {
			languages := dto.BuildMetadataLanguages(payload.Languages)
			res.VideoMetadata = dto.GenerateVideoMetadata(groupCtx, u.generator, payload.Title, languages, u.metadataModel)
			return nil
		})
	}

	if waitErr := group.Wait(); waitErr != nil {
		if u.log != nil {
			u.log.Warn("post-generation phase returned an error (continuing)", zap.Error(waitErr))
		}
	}

	return res, nil
}
