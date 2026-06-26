// Package scripts — postgen_usecase.go is the post-generation phase
// for the unified clip-source script generation job.
package scripts

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

type PostGenResult struct {
	EntitiesJSON  string
	Insights      ScriptInsights
	VideoMetadata []VideoMetadata
}

type InsightBuilder interface {
	Build(ctx context.Context, title, script, entitiesJSON string) ScriptInsights
}

type PostGenUseCase struct {
	entities      *EntityExtractionUtility
	generator     *ollama.Generator
	metadataModel string
	log           *zap.Logger
}

func NewPostGenUseCase(
	extractor EntityScriptExtractor,
	insightBuilder InsightBuilder,
	generator *ollama.Generator,
	metadataModel string,
	log *zap.Logger,
) *PostGenUseCase {
	return &PostGenUseCase{
		entities:      NewEntityExtractionUtility(extractor, insightBuilder, metadataModel, log),
		generator:     generator,
		metadataModel: metadataModel,
		log:           log,
	}
}

func (u *PostGenUseCase) Run(ctx context.Context, payload *scriptpkg.GenerationSpec, script string) (PostGenResult, error) {
	var result PostGenResult
	if u == nil || payload == nil || script == "" {
		return result, nil
	}
	if !payload.ExtractEntities && !payload.GenerateMetadata {
		return result, nil
	}

	group, groupCtx := concurrent.WithContext(ctx)
	if payload.ExtractEntities {
		group.Go("entities-and-insights", func() error {
			entities, err := u.entities.Run(groupCtx, payload.Title, script, "")
			if err != nil {
				if u.log != nil {
					u.log.Warn("entity extraction failed", zap.Error(err))
				}
				return nil
			}
			result.EntitiesJSON = entities.EntitiesJSON
			result.Insights = entities.Insights
			return nil
		})
	}
	if payload.GenerateMetadata && u.generator != nil {
		group.Go("video-metadata", func() error {
			languages := BuildMetadataLanguages(payload.Languages)
			result.VideoMetadata = GenerateVideoMetadata(groupCtx, u.generator, payload.Title, languages, u.metadataModel)
			return nil
		})
	}
	if waitErr := group.Wait(); waitErr != nil && u.log != nil {
		u.log.Warn("post-generation phase returned an error (continuing)", zap.Error(waitErr))
	}
	return result, nil
}
