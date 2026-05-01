package association

import (
	"context"
)

// SegmentInput provides data for association matching.
type SegmentInput struct {
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
	var allMatches []ScoredMatch
	for _, source := range e.sources {
		if source == nil {
			continue
		}
		if matches, err := source.Associate(ctx, input); err == nil {
			allMatches = append(allMatches, matches...)
		}
	}
	return allMatches
}
