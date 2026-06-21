package qdrant

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// parseSearchResults decodes Qdrant /points/search response into SearchResult slice.
// The /points/search endpoint returns result[] directly.
func parseSearchResults(respBody []byte, minScore float64, limit int) ([]SearchResult, error) {
	var qdrantResp struct {
		Result []rawPoint `json:"result"`
	}
	if err := json.Unmarshal(respBody, &qdrantResp); err != nil {
		return nil, fmt.Errorf("parse search response: %w", err)
	}
	return filterAndBuildResults(qdrantResp.Result, minScore, limit), nil
}

// parseQueryResults decodes Qdrant /points/query response into SearchResult slice.
// The /points/query endpoint (Qdrant >= 1.7) returns result.points[] (nested).
func parseQueryResults(respBody []byte, minScore float64, limit int) ([]SearchResult, error) {
	var qdrantResp struct {
		Result struct {
			Points []rawPoint `json:"points"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &qdrantResp); err != nil {
		return nil, fmt.Errorf("parse query response: %w", err)
	}
	return filterAndBuildResults(qdrantResp.Result.Points, minScore, limit), nil
}

// rawPoint is the common shape for both /points/search and /points/query results.
type rawPoint struct {
	ID      any            `json:"id"`
	Score   float64        `json:"score"`
	Payload map[string]any `json:"payload"`
}

// filterAndBuildResults converts raw Qdrant points to SearchResult slices,
// applying minScore filtering and limit truncation.
func filterAndBuildResults(points []rawPoint, minScore float64, limit int) []SearchResult {
	results := make([]SearchResult, 0, len(points))
	for _, r := range points {
		if r.Score < minScore {
			continue
		}
		idStr := idToString(r.ID)
		results = append(results, searchResultFromPayload(idStr, r.Score, r.Payload))
	}
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

// idToString converts a Qdrant point ID (which may be uint64 or UUID string)
// to a string for internal use.
func idToString(id any) string {
	switch v := id.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', 0, 64)
	case json.Number:
		return string(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// extractString safely extracts a string value from a Qdrant payload map.
func extractString(payload map[string]any, key string) string {
	if p, ok := payload[key]; ok {
		if s, ok := p.(string); ok {
			return s
		}
	}
	return ""
}

// extractTags extracts a string slice from a Qdrant payload, handling both
// Go []string and JSON []any representations.
func extractTags(payload map[string]any, key string) []string {
	p, ok := payload[key]
	if !ok {
		return nil
	}
	switch t := p.(type) {
	case []any:
		var tags []string
		for _, tag := range t {
			if s, ok := tag.(string); ok {
				tags = append(tags, s)
			}
		}
		return tags
	case []string:
		return t
	}
	return nil
}

// searchResultFromPayload builds a SearchResult from a Qdrant point ID, score, and payload map.
// The logical asset_id is stored in the payload; the pointID is the technical Qdrant ID.
func searchResultFromPayload(pointID string, score float64, payload map[string]any) SearchResult {
	assetID := extractString(payload, "asset_id")
	if assetID == "" {
		assetID = pointID
	}

	return SearchResult{
		AssetID:           assetID,
		QdrantPointID:     pointID,
		Score:             score,
		Source:            extractString(payload, "source"),
		Name:              extractString(payload, "name"),
		LocalPath:         extractString(payload, "local_path"),
		DriveLink:         extractString(payload, "drive_link"),
		Category:          extractString(payload, "category"),
		MediaType:         extractString(payload, "media_type"),
		Style:             extractString(payload, "style"),
		Language:          extractString(payload, "language"),
		SearchText:        extractString(payload, "search_text"),
		EmbeddingVersion:  extractString(payload, "embedding_version"),
		SearchTextVersion: extractString(payload, "search_text_version"),
		YouTubeVideoID:    extractString(payload, "youtube_video_id"),
		YouTubeURL:        extractString(payload, "youtube_url"),
		StartTime:         extractString(payload, "start_time"),
		EndTime:           extractString(payload, "end_time"),
		Tags:              extractTags(payload, "tags"),
	}
}
