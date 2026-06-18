package clips

import (
	"encoding/json"
	"strconv"
	"strings"
)

func metadataString(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	raw, ok := meta[key]
	if !ok || raw == nil {
		return ""
	}
	if s, ok := raw.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func metadataBool(meta map[string]any, key string) bool {
	if meta == nil {
		return false
	}
	raw, ok := meta[key]
	if !ok || raw == nil {
		return false
	}
	if v, ok := raw.(bool); ok {
		return v
	}
	if s, ok := raw.(string); ok {
		return strings.EqualFold(strings.TrimSpace(s), "true")
	}
	return false
}

func metadataFloat(meta map[string]any, key string) float64 {
	if meta == nil {
		return 0
	}
	raw, ok := meta[key]
	if !ok || raw == nil {
		return 0
	}
	switch v := raw.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f
	}
	return 0
}

func metadataStringSlice(meta map[string]any, key string) []string {
	if meta == nil {
		return nil
	}
	raw, ok := meta[key]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		var out []string
		if err := json.Unmarshal([]byte(v), &out); err == nil {
			return out
		}
	}
	return nil
}

func metadataStringSliceText(meta map[string]any, key string) string {
	values := metadataStringSlice(meta, key)
	return strings.Join(values, " ")
}

// clipSearchColumns returns the SQL column list used by LIKE-based search
// across the catalog. After migration 059, search_text is a canonical
// column; the other JSON-derived helpers (clean_title, clip_summary, hook,
// topics, speakers, mentioned_people, people, clip_tags, search_keywords,
// embedding_text) stay in metadata_json because they are populated by the
// clipindexer PIPELINE and are too transient to justify a typed column.
func clipSearchColumns() []string {
	return []string{
		"tags",
		"name",
		"search_text",
		"json_extract(COALESCE(metadata_json,'{}'), '$.clean_title')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.clip_summary')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.hook')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.topics')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.speakers')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.mentioned_people')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.people')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.clip_tags')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.search_keywords')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.embedding_text')",
	}
}
