package association

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// SegmentInput provides data for association matching.
type SegmentInput struct {
	Topic     string
	Subject   string
	Keywords  []string
	Entities  []string
	Narrative string
}

// Association defines the interface for different media sources.
type Association interface {
	Associate(ctx context.Context, input SegmentInput) ([]ScoredMatch, error)
}

// Engine orchestrates multiple associations to find the best matches.
type Engine struct {
	sources []Association
}

func NewEngine(sources ...Association) *Engine {
	return &Engine{sources: sources}
}

func (e *Engine) AssociateAll(ctx context.Context, input SegmentInput) []ScoredMatch {
	zap.L().Info("Engine.AssociateAll called",
		zap.Int("source_count", len(e.sources)),
		zap.String("topic", input.Topic),
		zap.String("subject", input.Subject),
	)

	var allMatches []ScoredMatch
	for i, source := range e.sources {
		if source == nil {
			zap.L().Warn("Engine: source is nil", zap.Int("index", i))
			continue
		}
		zap.L().Info("Engine: calling source", zap.Int("index", i), zap.String("type", fmt.Sprintf("%T", source)))
		if matches, err := source.Associate(ctx, input); err == nil {
			zap.L().Info("Engine: source returned matches", zap.Int("index", i), zap.Int("match_count", len(matches)))
			allMatches = append(allMatches, matches...)
		} else {
			zap.L().Warn("Engine: source returned error", zap.Int("index", i), zap.Error(err))
		}
	}
	return allMatches
}
