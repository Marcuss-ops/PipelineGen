package script

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/types"

	"go.uber.org/zap"
)

const (
	DefaultMaxQueries = 3
	MinQueryWords    = 2
	MaxQueryWords    = 4
	cacheVersion     = "v1"
)

// VisualQueryResult contains the enriched query result from LLM
type VisualQueryResult struct {
	VisualSubject string   `json:"visual_subject"`
	VisualCaption string   `json:"visual_caption"`
	Queries       []string `json:"queries"`
}

// queryCache provides in-memory caching for LLM-generated queries
var (
	queryCache   = make(map[string]VisualQueryResult)
	queryCacheMu sync.RWMutex
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
	if gen == nil {
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

	zap.L().Debug("GenerateArtlistVisualQuery: sending request to LLM",
		zap.String("prompt", prompt),
	)

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
	if gen == nil {
		zap.L().Warn("GenerateBatchArtlistVisualQueries: generator is nil, using fallback")
		return buildBatchFallback(topic, segments)
	}

	if maxQueriesPerSegment <= 0 {
		maxQueriesPerSegment = DefaultMaxQueries
	}

	// Check cache first
	results := make(map[int]VisualQueryResult)
	uncachedSegments := make([]BatchSegmentInput, 0)
	uncachedIndices := make([]int, 0)

	for _, seg := range segments {
		cacheKey := buildCacheKey(topic, seg.Subject, seg.Narrative, maxQueriesPerSegment)
		if cached, ok := getFromCache(cacheKey); ok {
			results[seg.Index] = cached
		} else {
			uncachedSegments = append(uncachedSegments, seg)
			uncachedIndices = append(uncachedIndices, seg.Index)
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
		for _, idx := range uncachedIndices {
			for _, seg := range uncachedSegments {
				if seg.Index == idx {
					queries := buildFallbackQueries(topic, seg.Subject, seg.Narrative)
					results[idx] = VisualQueryResult{
						VisualSubject: seg.Subject,
						VisualCaption: seg.Narrative,
						Queries:       queries,
					}
					break
				}
			}
		}
		return results
	}

	// Parse batch response
	parsedResults := parseBatchVisualQueryResponse(response, uncachedSegments, maxQueriesPerSegment)
	for i, idx := range uncachedIndices {
		if i < len(parsedResults) {
			results[idx] = parsedResults[i]
			// Save to cache
			seg := uncachedSegments[i]
			cacheKey := buildCacheKey(topic, seg.Subject, seg.Narrative, maxQueriesPerSegment)
			saveToCache(cacheKey, parsedResults[i])
		}
	}

	return results
}

// BatchSegmentInput represents a segment for batch processing
type BatchSegmentInput struct {
	Index     int
	Subject   string
	Narrative string
}

// parseVisualQueryResponse parses the LLM response into VisualQueryResult
func parseVisualQueryResponse(response string, subject string, narrative string, maxQueries int) VisualQueryResult {
	response = strings.TrimSpace(response)
	
	// Try to extract JSON from response
	jsonStart := strings.Index(response, "{")
	jsonEnd := strings.LastIndex(response, "}")
	
	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
		zap.L().Warn("parseVisualQueryResponse: no valid JSON object found")
		return VisualQueryResult{
			VisualSubject: subject,
			VisualCaption: narrative,
			Queries:       nil,
		}
	}
	
	jsonStr := response[jsonStart : jsonEnd+1]
	
	var result VisualQueryResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		zap.L().Warn("parseVisualQueryResponse: JSON parse failed",
			zap.Error(err),
			zap.String("json", truncateString(jsonStr, 100)),
		)
		return VisualQueryResult{
			VisualSubject: subject,
			VisualCaption: narrative,
			Queries:       nil,
		}
	}
	
	// Validate and filter queries
	validated := make([]string, 0, len(result.Queries))
	seen := make(map[string]bool)
	for _, q := range result.Queries {
		q = strings.TrimSpace(q)
		if seen[q] {
			continue
		}
		if isValidVisualQuery(q) {
			validated = append(validated, q)
			seen[q] = true
		}
		if len(validated) >= maxQueries {
			break
		}
	}
	result.Queries = validated
	
	if result.VisualSubject == "" {
		result.VisualSubject = subject
	}
	if result.VisualCaption == "" {
		result.VisualCaption = narrative
	}
	
	return result
}

// parseBatchVisualQueryResponse parses batch LLM response
func parseBatchVisualQueryResponse(response string, segments []BatchSegmentInput, maxQueries int) map[int]VisualQueryResult {
	response = strings.TrimSpace(response)
	
	jsonStart := strings.Index(response, "[")
	jsonEnd := strings.LastIndex(response, "]")
	
	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
		zap.L().Warn("parseBatchVisualQueryResponse: no valid JSON array found")
		return buildBatchFallback("", segments)
	}
	
	jsonStr := response[jsonStart : jsonEnd+1]
	
	var batchResults []map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &batchResults); err != nil {
		zap.L().Warn("parseBatchVisualQueryResponse: JSON parse failed",
			zap.Error(err),
		)
		return buildBatchFallback("", segments)
	}
	
	results := make(map[int]VisualQueryResult)
	for i, item := range batchResults {
		subject := ""
		narrative := ""
		segIndex := -1
		if i < len(segments) {
			subject = segments[i].Subject
			narrative = segments[i].Narrative
			segIndex = segments[i].Index
		}
		
		result := VisualQueryResult{
			VisualSubject: subject,
			VisualCaption: narrative,
			Queries:       nil,
		}
		
		if v, ok := item["visual_subject"].(string); ok {
			result.VisualSubject = v
		}
		if v, ok := item["visual_caption"].(string); ok {
			result.VisualCaption = v
		}
		if v, ok := item["queries"].([]interface{}); ok {
			validated := make([]string, 0, len(v))
			seen := make(map[string]bool)
			for _, q := range v {
				if qs, ok := q.(string); ok {
					qs = strings.TrimSpace(qs)
					if !seen[qs] && isValidVisualQuery(qs) {
						validated = append(validated, qs)
						seen[qs] = true
					}
					if len(validated) >= maxQueries {
						break
					}
				}
			}
			result.Queries = validated
		}
		
		if segIndex >= 0 {
			results[segIndex] = result
		}
	}
	
	return results
}

// buildVisualQueryPrompt creates the prompt for visual query generation
// Returns enriched JSON with visual_subject, visual_caption, and queries
func buildVisualQueryPrompt(topic, subject, narrative string) string {
	return fmt.Sprintf(`You are generating search queries for stock video platforms like Artlist.

Given a documentary sentence, create a JSON object with visual subject, visual caption, and %d short visual search queries.

Rules:
- visual_subject: 2-4 words summarizing the visual theme
- visual_caption: 5-15 words describing what should be shown visually
- queries: array of %d short search queries (2-4 words each)
- Use concrete visual concepts, not abstract ideas
- Avoid filler words (the, a, an, is, was, were, are, been, have, has, had, but, and, or)
- Avoid full sentences in queries
- Prefer scenes, objects, environments, actions, historical period, scientific setting
- Return only valid JSON object

Examples:

Input: "Further excavation is planned, and with each new layer of rock exposed..."
Output: {"visual_subject": "archaeological excavation", "visual_caption": "Archaeologists carefully uncovering ancient rock layers and fossils", "queries": ["archaeological excavation", "ancient cave discovery", "rock layer excavation"]}

Input: "Analysis of pollen and charcoal fragments within the chambers..."
Output: {"visual_subject": "laboratory analysis", "visual_caption": "Scientists examining prehistoric organic samples under bright lights", "queries": ["archaeology laboratory", "ancient sample analysis", "prehistoric cave painting"]}

Sentence: "%s"
Context topic: "%s"
Segment subject: "%s"

JSON:`, DefaultMaxQueries, DefaultMaxQueries, narrative, topic, subject)
}

// buildBatchVisualQueryPrompt creates a batch prompt for multiple segments
func buildBatchVisualQueryPrompt(topic string, segments []BatchSegmentInput, maxQueries int) string {
	segmentsJSON, _ := json.Marshal(segments)
	return fmt.Sprintf(`You are generating search queries for stock video platforms like Artlist.

Given an array of documentary segments, create a JSON array where each element has visual_subject, visual_caption, and queries for the corresponding segment.

Rules:
- visual_subject: 2-4 words summarizing the visual theme
- visual_caption: 5-15 words describing what should be shown visually
- queries: array of up to %d short search queries (2-4 words each)
- Use concrete visual concepts, not abstract ideas
- Avoid filler words
- Return only valid JSON array

Segments: %s

Output JSON array:`, maxQueries, string(segmentsJSON))
}

// buildCacheKey creates a unique cache key for query generation
func buildCacheKey(topic, subject, narrative string, maxQueries int) string {
	// Simple hash of inputs
	hashInput := fmt.Sprintf("%s|%s|%s|%d|%s", topic, subject, narrative, maxQueries, cacheVersion)
	return fmt.Sprintf("%x", hash(hashInput))
}

// hash creates a simple hash of the input string
func hash(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// getFromCache retrieves a cached result
func getFromCache(key string) (VisualQueryResult, bool) {
	queryCacheMu.RLock()
	defer queryCacheMu.RUnlock()
	
	result, ok := queryCache[key]
	return result, ok
}

// saveToCache stores a result in cache
func saveToCache(key string, result VisualQueryResult) {
	queryCacheMu.Lock()
	defer queryCacheMu.Unlock()
	
	queryCache[key] = result
}

// buildBatchFallback creates fallback results for batch processing
func buildBatchFallback(topic string, segments []BatchSegmentInput) map[int]VisualQueryResult {
	results := make(map[int]VisualQueryResult)
	for _, seg := range segments {
		queries := buildFallbackQueries(topic, seg.Subject, seg.Narrative)
		results[seg.Index] = VisualQueryResult{
			VisualSubject: seg.Subject,
			VisualCaption: seg.Narrative,
			Queries:       queries,
		}
	}
	return results
}

// isValidVisualQuery validates that a query meets the requirements
func isValidVisualQuery(query string) bool {
	if query == "" {
		return false
	}

	// Check for punctuation (except spaces and hyphens)
	for _, r := range query {
		if !isAllowedChar(r) {
			return false
		}
	}

	words := strings.Fields(query)
	
	// Check word count
	if len(words) < MinQueryWords || len(words) > MaxQueryWords {
		return false
	}

	// Check for banned words
	bannedWords := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "was": true,
		"were": true, "are": true, "been": true, "have": true, "has": true,
		"had": true, "but": true, "and": true, "or": true, "then": true,
		"they": true, "their": true, "further": true, "each": true,
		"continues": true, "beginning": true, "comprehend": true,
	}

	for _, w := range words {
		if bannedWords[strings.ToLower(w)] {
			return false
		}
	}

	return true
}

// isAllowedChar checks if a character is allowed in a query
func isAllowedChar(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == ' ' || r == '-'
}

// buildFallbackQueries creates fallback queries when LLM fails
func buildFallbackQueries(topic, subject, narrative string) []string {
	queries := make([]string, 0, DefaultMaxQueries)

	base := firstNonEmpty(subject, topic)
	if base != "" {
		queries = append(queries, base)
	}

	if len(queries) < DefaultMaxQueries && narrative != "" {
		words := strings.Fields(narrative)
		if len(words) > 3 {
			candidate := strings.Join(words[:3], " ")
			if isValidVisualQuery(candidate) {
				queries = append(queries, candidate)
			}
		}
	}

	if len(queries) == 0 {
		queries = append(queries, "documentary landscape")
	}

	return queries
}

// truncateString truncates a string to maxLen characters
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
