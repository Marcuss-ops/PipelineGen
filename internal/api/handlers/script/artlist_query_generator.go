package script

import (
	"context"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/media/visualquery"
)

// GenerateArtlistSearchSuggestions generates visual search queries using LLM
// Deprecated: Use visualquery.GenerateArtlistSearchSuggestions instead
func GenerateArtlistSearchSuggestions(
	ctx context.Context,
	gen *ollama.Generator,
	topic string,
	subject string,
	narrative string,
	maxQueries int,
) []string {
	return visualquery.GenerateArtlistSearchSuggestions(ctx, gen, topic, subject, narrative, maxQueries)
}

// GenerateArtlistVisualQuery generates enriched visual query result using LLM
// Deprecated: Use visualquery.GenerateArtlistVisualQuery instead
func GenerateArtlistVisualQuery(
	ctx context.Context,
	gen *ollama.Generator,
	topic string,
	subject string,
	narrative string,
	maxQueries int,
) visualquery.VisualQueryResult {
	return visualquery.GenerateArtlistVisualQuery(ctx, gen, topic, subject, narrative, maxQueries)
}

// GenerateBatchArtlistVisualQueries generates visual queries for multiple segments in a single LLM call
// Deprecated: Use visualquery.GenerateBatchArtlistVisualQueries instead
func GenerateBatchArtlistVisualQueries(
	ctx context.Context,
	gen *ollama.Generator,
	topic string,
	segments []visualquery.BatchSegmentInput,
	maxQueriesPerSegment int,
) map[int]visualquery.VisualQueryResult {
	return visualquery.GenerateBatchArtlistVisualQueries(ctx, gen, topic, segments, maxQueriesPerSegment)
}
