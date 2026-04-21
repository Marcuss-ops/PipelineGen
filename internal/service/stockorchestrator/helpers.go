package stockorchestrator

import (
	"encoding/json"

	"velox/go-master/internal/stock"
	"velox/go-master/pkg/util"
)

func convertToYouTubeResults(results []stock.VideoResult) []YouTubeResult {
	out := make([]YouTubeResult, len(results))
	for i, r := range results {
		out[i] = YouTubeResult{
			ID:          r.ID,
			Title:       r.Title,
			URL:         r.URL,
			Duration:    r.Duration,
			Thumbnail:   r.Thumbnail,
			Description: r.Description,
		}
	}
	return out
}

func parseJSON(str string, v interface{}) error {
	return json.Unmarshal([]byte(str), v)
}

func sanitizeFilename(name string) string {
	result := ""
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == ' ' {
			result += string(c)
		} else {
			result += "_"
		}
	}
	return result[:util.Min(len(result), 100)] // Limit length
}
