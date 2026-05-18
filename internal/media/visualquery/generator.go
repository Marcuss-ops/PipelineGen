package visualquery

import (
	"context"

	"go.uber.org/zap"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/types"
)

// GenerateArtlistSearchSuggestions generates visual search queries using LLM
func GenerateArtlistSearchSuggestions(
	ctx context.Context,
	gen *ollama.Generator,
	topic string,
	subject string,
	narrative string,
	maxQueries int,
) []string {
	result := GenerateArtlistVisualQuery(ctx, gen, topic, subject, narrative, maxQueries)
	return result.Queries
}

// GenerateArtlistVisualQuery generates enriched visual query result using LLM
func GenerateArtlistVisualQuery(
	ctx context.Context,
	gen *ollama.Generator,
	topic string,
	subject string,
	narrative string,
	maxQueries int,
) VisualQueryResult {
	if gen == nil || gen.GetClient() == nil {
		zap.L().Warn("GenerateArtlistVisualQuery: generator is nil, using fallback")
		queries := buildFallbackQueries(topic, subject, narrative)
		return VisualQueryResult{
			VisualSubject: subject,
			VisualCaption: narrative,
			Queries:       queries,
		}
	}

	if maxQueries <= 0 {
		maxQueries = DefaultMaxQueries
	}

	// Check cache
	cacheKey := buildCacheKey(topic, subject, narrative, maxQueries)
	if cached, ok := getFromCache(cacheKey); ok {
		zap.L().Info("GenerateArtlistVisualQuery: cache hit",
			zap.String("cache_key", cacheKey),
		)
		return cached
	}

	zap.L().Info("GenerateArtlistVisualQuery: starting LLM query generation",
		zap.String("topic", topic),
		zap.String("subject", subject),
		zap.Int("narrative_length", len(narrative)),
		zap.Int("max_queries", maxQueries),
	)

	prompt := buildVisualQueryPrompt(topic, subject, narrative)

	messages := []types.Message{
		{
			Role:    "system",
			Content: "You are a visual search query generator for stock video platforms like Artlist. Return only valid JSON.",
		},
		{
			Role:    "user",
			Content: prompt,
		},
	}

	response, err := gen.GetClient().Chat(ctx, messages, nil)
	if err != nil {
		zap.L().Error("GenerateArtlistVisualQuery: LLM call failed, using fallback",
			zap.Error(err),
		)
		queries := buildFallbackQueries(topic, subject, narrative)
		return VisualQueryResult{
			VisualSubject: subject,
			VisualCaption: narrative,
			Queries:       queries,
		}
	}

	zap.L().Info("GenerateArtlistVisualQuery: LLM response received",
		zap.Int("response_length", len(response)),
		zap.String("response_preview", truncateString(response, 200)),
	)

	result := parseVisualQueryResponse(response, subject, narrative, maxQueries)

	if len(result.Queries) == 0 {
		zap.L().Warn("GenerateArtlistVisualQuery: no valid queries from LLM, using fallback")
		queries := buildFallbackQueries(topic, subject, narrative)
		result = VisualQueryResult{
			VisualSubject: subject,
			VisualCaption: narrative,
			Queries:       queries,
		}
	}

	// Save to cache
	saveToCache(cacheKey, result)

	zap.L().Info("GenerateArtlistVisualQuery: successfully generated visual query",
		zap.String("visual_subject", result.VisualSubject),
		zap.Strings("queries", result.Queries),
	)

	return result
}

// GenerateBatchArtlistVisualQueries generates visual queries for multiple segments in a single LLM call
func GenerateBatchArtlistVisualQueries(
	ctx context.Context,
	gen *ollama.Generator,
	topic string,
	segments []BatchSegmentInput,
	maxQueriesPerSegment int,
) map[int]VisualQueryResult {
	if gen == nil || gen.GetClient() == nil {
		zap.L().Warn("GenerateBatchArtlistVisualQueries: generator is nil, using fallback")
		return buildBatchFallback(topic, segments)
	}

	if maxQueriesPerSegment <= 0 {
		maxQueriesPerSegment = DefaultMaxQueries
	}

	// Check cache first
	results := make(map[int]VisualQueryResult)
	uncachedSegments := make([]BatchSegmentInput, 0)

	for _, seg := range segments {
		cacheKey := buildCacheKey(topic, seg.Subject, seg.Narrative, maxQueriesPerSegment)
		if cached, ok := getFromCache(cacheKey); ok {
			results[seg.Index] = cached
		} else {
			uncachedSegments = append(uncachedSegments, seg)
		}
	}

	if len(uncachedSegments) == 0 {
		zap.L().Info("GenerateBatchArtlistVisualQueries: all results from cache")
		return results
	}

	zap.L().Info("GenerateBatchArtlistVisualQueries: calling LLM for uncached segments",
		zap.Int("uncached_count", len(uncachedSegments)),
	)

	prompt := buildBatchVisualQueryPrompt(topic, uncachedSegments, maxQueriesPerSegment)

	messages := []types.Message{
		{
			Role:    "system",
			Content: "You are a visual search query generator for stock video platforms like Artlist. Return only valid JSON array.",
		},
		{
			Role:    "user",
			Content: prompt,
		},
	}

	response, err := gen.GetClient().Chat(ctx, messages, nil)
	if err != nil {
		zap.L().Error("GenerateBatchArtlistVisualQueries: LLM call failed, using fallback",
			zap.Error(err),
		)
		for _, seg := range uncachedSegments {
			queries := buildFallbackQueries(topic, seg.Subject, seg.Narrative)
			results[seg.Index] = VisualQueryResult{
				VisualSubject: seg.Subject,
				VisualCaption: seg.Narrative,
				Queries:       queries,
			}
		}
		return results
	}

	// Parse batch response
	parsedResults := parseBatchVisualQueryResponse(response, uncachedSegments, maxQueriesPerSegment)
	for _, seg := range uncachedSegments {
		segIdx := seg.Index
		if result, ok := parsedResults[segIdx]; ok {
			results[segIdx] = result
			// Save to cache
			cacheKey := buildCacheKey(topic, seg.Subject, seg.Narrative, maxQueriesPerSegment)
			saveToCache(cacheKey, result)
		}
	}

	return results
}
