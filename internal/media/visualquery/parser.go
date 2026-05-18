package visualquery

import (
	"encoding/json"
	"strings"

	"go.uber.org/zap"
)

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
			result.Queries = parseInterfaceSlice(v, maxQueries)
		}
		if v, ok := item["entity_queries"].([]interface{}); ok {
			result.EntityQueries = parseInterfaceSlice(v, 3)
		}
		if v, ok := item["visual_prompts"].([]interface{}); ok {
			result.VisualPrompts = parseInterfaceSlice(v, 3)
		}

		if segIndex >= 0 {
			results[segIndex] = result
		}
	}

	return results
}

func parseInterfaceSlice(v []interface{}, limit int) []string {
	validated := make([]string, 0, len(v))
	seen := make(map[string]bool)
	for _, q := range v {
		if qs, ok := q.(string); ok {
			qs = strings.TrimSpace(qs)
			if qs != "" && !seen[qs] {
				validated = append(validated, qs)
				seen[qs] = true
			}
			if len(validated) >= limit {
				break
			}
		}
	}
	return validated
}
