package nlp

import (
	"context"
	"fmt"
	"strings"

	entityports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities/ports"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type EntityExtractorAdapter struct{ backend scriptgen.NERBackend }

func NewEntityExtractorAdapter(backend scriptgen.NERBackend) (*EntityExtractorAdapter, error) {
	if backend == nil {
		return nil, fmt.Errorf("entity extractor adapter: NER backend is required")
	}
	return &EntityExtractorAdapter{backend: backend}, nil
}

func (a *EntityExtractorAdapter) ExtractEntities(ctx context.Context, req scriptpkg.EntityExtractionRequest) (*scriptpkg.EntityResult, error) {
	if a == nil || a.backend == nil {
		return nil, fmt.Errorf("entity extractor adapter: NER backend is not configured")
	}
	if strings.TrimSpace(req.Language) == "" {
		return nil, fmt.Errorf("entity extractor adapter: language is required")
	}
	limit := req.EntityCount
	if limit <= 0 {
		limit = 3
	}
	entities, err := a.backend.Extract(ctx, req.Language, req.Text, limit)
	if err != nil {
		return nil, err
	}
	result := &scriptpkg.EntityResult{NounChunks: make([]string, 0, len(entities)), Concepts: make([]scriptpkg.Entity, 0, len(entities))}
	for _, entity := range entities {
		if entity.Start < 0 || entity.End <= entity.Start || entity.End > len(req.Text) || req.Text[entity.Start:entity.End] != entity.Text || entity.Evidence != "" && entity.Evidence != entity.Text {
			return nil, fmt.Errorf("entity extractor adapter: entity %q is not source grounded", entity.Text)
		}
		result.NounChunks = append(result.NounChunks, entity.Text)
		result.Concepts = append(result.Concepts, scriptpkg.Entity{Value: entity.Text, Type: string(entity.Type), Score: entity.Score})
	}
	return result, nil
}

var _ entityports.EntityExtractor = (*EntityExtractorAdapter)(nil)
