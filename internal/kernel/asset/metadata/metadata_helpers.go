package metadata

import (
	"encoding/json"
	"strconv"
	"strings"
)

// MetadataString extracts a string-typed value from a metadata map.
// Returns "" when the map is nil, the key is absent, or the value is not a string.
// Trims whitespace from the result.
func MetadataString(meta map[string]any, key string) string {
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

// MetadataBool extracts a bool-typed value from a metadata map.
// Accepts both bool and string ("true"/"false", case-insensitive).
// Returns false when the map is nil, the key is absent, or the value
// cannot be interpreted as a boolean.
func MetadataBool(meta map[string]any, key string) bool {
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

// MetadataFloat extracts a float64-typed value from a metadata map.
// Accepts float64, float32, int, int64, and string (via strconv.ParseFloat).
// Also handles json.Number (fmt.Stringer) via the string path.
// Returns 0 when the map is nil, the key is absent, or the value
// cannot be interpreted as a float.
func MetadataFloat(meta map[string]any, key string) float64 {
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
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f
		}
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f
	}
	return 0
}

// MetadataStringSlice extracts a []string-typed value from a metadata map.
// Accepts []string (direct), []any (each element cast to string), and string
// (JSON-encoded array, e.g. `["a","b"]`).
// Returns nil when the map is nil, the key is absent, or the value
// cannot be interpreted as a string slice.
func MetadataStringSlice(meta map[string]any, key string) []string {
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

// MetadataInt extracts an int-typed value from a metadata map.
// Accepts int, int32, int64, float64 (truncated), json.Number, and string
// (via strconv.Atoi). Returns 0 when the map is nil, the key is absent,
// or the value cannot be interpreted as an integer.
func MetadataInt(meta map[string]any, key string) int {
	if meta == nil {
		return 0
	}
	raw, ok := meta[key]
	if !ok || raw == nil {
		return 0
	}
	switch v := raw.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return i
		}
	}
	return 0
}

// MetadataStringSliceText joins a metadata string slice into a single
// space-separated string. Convenience wrapper around MetadataStringSlice.
func MetadataStringSliceText(meta map[string]any, key string) string {
	values := MetadataStringSlice(meta, key)
	return strings.Join(values, " ")
}

// ── Internal aliases (keep existing unexported callers working) ──────────

func metadataString(meta map[string]any, key string) string { return MetadataString(meta, key) }
func metadataBool(meta map[string]any, key string) bool     { return MetadataBool(meta, key) }
func metadataFloat(meta map[string]any, key string) float64 { return MetadataFloat(meta, key) }
func metadataStringSlice(meta map[string]any, key string) []string {
	return MetadataStringSlice(meta, key)
}
func metadataStringSliceText(meta map[string]any, key string) string {
	return MetadataStringSliceText(meta, key)
}
