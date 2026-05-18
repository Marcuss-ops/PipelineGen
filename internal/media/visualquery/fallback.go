package visualquery

import (
	"strings"
)

// buildFallbackQueries creates fallback queries when LLM fails
func buildFallbackQueries(topic, subject, narrative string) []string {
	queries := make([]string, 0, DefaultMaxQueries)

	// Use subject first, then topic
	base := FirstNonEmpty(subject, topic)
	if base != "" {
		queries = append(queries, base)
	}

	// If still need more, add topic if not already present
	if len(queries) < DefaultMaxQueries && topic != "" && !strings.EqualFold(base, topic) {
		queries = append(queries, topic)
	}

	if len(queries) == 0 {
		queries = append(queries, "documentary landscape")
	}

	// Truncate to max queries
	if len(queries) > DefaultMaxQueries {
		queries = queries[:DefaultMaxQueries]
	}

	return queries
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
