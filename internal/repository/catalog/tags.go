package catalog

import (
	"encoding/json"
	"strings"

	"velox/go-master/pkg/textutil"
)

// ParseTags parses a string of tags that might be JSON array or CSV.
func ParseTags(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	
	// If it looks like a JSON array, extract and parse it
	if strings.HasPrefix(raw, "[") {
		jsonStr := textutil.ExtractJSONArray(raw)
		if jsonStr != "" {
			var tags []string
			if err := json.Unmarshal([]byte(jsonStr), &tags); err == nil {
				return textutil.NormalizeStringSlice(tags)
			}
		}
	}
	
	// Fallback to CSV
	tags := textutil.SplitCSV(raw)
	return textutil.NormalizeStringSlice(tags)
}
