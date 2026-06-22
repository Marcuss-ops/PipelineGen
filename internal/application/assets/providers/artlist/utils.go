package artlist

import "strings"

// getIntFromResult extracts an int from a result map, handling both int and float64 types
func getIntFromResult(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	default:
		return 0
	}
}

// bestPexelsVideoURL selects the best-quality video URL from Pexels video files.
func bestPexelsVideoURL(files []struct {
	ID       int     `json:"id"`
	Quality  string  `json:"quality"`
	FileType string  `json:"file_type"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	FPS      float64 `json:"fps"`
	Link     string  `json:"link"`
}) string {
	var bestURL string
	bestScore := -1
	for _, f := range files {
		if strings.TrimSpace(f.Link) == "" {
			continue
		}
		score := f.Width * f.Height
		if strings.EqualFold(f.Quality, "hd") {
			score += 1_000_000
		}
		if score > bestScore {
			bestScore = score
			bestURL = f.Link
		}
	}
	return bestURL
}
