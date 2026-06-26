package scripts

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
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

func NewPostGenUseCase(extractor EntityScriptExtractor, insightBuilder InsightBuilder, generator *ollama.Generator, metadataModel string, log *zap.Logger) *PostGenUseCase {
	return &PostGenUseCase{entities: NewEntityExtractionUtility(extractor, insightBuilder, metadataModel, log), generator: generator, metadataModel: metadataModel, log: log}
}

func (u *PostGenUseCase) Run(ctx context.Context, payload *scriptpkg.GenerationSpec, script string) (PostGenResult, error) {
	var result PostGenResult
	if u == nil || payload == nil || script == "" {
		return result, nil
	}
	if payload.ExtractEntities {
		entities, err := u.entities.Run(ctx, payload.Title, script, payload.Model)
		if err != nil {
			if u.log != nil {
				u.log.Warn("entity extraction failed", zap.Error(err))
			}
		} else {
			result.EntitiesJSON = entities.EntitiesJSON
			result.Insights = entities.Insights
		}
	}
	if payload.GenerateMetadata && u.generator != nil {
		languages := BuildMetadataLanguages(payload.Languages)
		result.VideoMetadata = GenerateVideoMetadata(ctx, u.generator, payload.Title, languages, u.metadataModel)
	}
	return result, nil
}
