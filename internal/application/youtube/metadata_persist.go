package youtube

import (
	tagutil "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/tagutil"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ── Metadata helpers ────────────────────────────────────────────────────────

// ymDescription returns a cleaned YouTube description for human-readable metadata.
// It intentionally strips sponsor/link boilerplate so Drive metadata stays clip-focused.
func ymDescription(ym *DownloaderMetadata, clip *asset.Asset) string {
	if ym != nil && ym.Description != "" {
		return tagutil.CompactYouTubeDescription(ym.Description)
	}
	desc := clip.GetMetadataString("youtube_description")
	if desc != "" {
		return tagutil.CompactYouTubeDescription(desc)
	}
	return ""
}

// ymTags returns tags from ym or falls back to clip DB metadata.
func ymTags(ym *DownloaderMetadata, clip *asset.Asset) []string {
	if ym != nil && len(ym.Tags) > 0 {
		return tagutil.NormalizeClipTagList(ym.Tags)
	}
	tagsJSON := clip.GetMetadataString("youtube_tags")
	if tagsJSON != "" && tagsJSON != "[]" {
		var tags []string
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err == nil {
			return tagutil.NormalizeClipTagList(tags)
		}
	}
	// Fallback to clip.Tags
	if len(clip.Tags) > 0 {
		return tagutil.NormalizeClipTagList(clip.Tags)
	}
	return nil
}

// ymCategories returns categories from ym or falls back to clip DB metadata.
func ymCategories(ym *DownloaderMetadata, clip *asset.Asset) []string {
	if ym != nil && len(ym.Categories) > 0 {
		return ym.Categories
	}
	catsJSON := clip.GetMetadataString("youtube_categories")
	if catsJSON != "" && catsJSON != "[]" {
		var cats []string
		json.Unmarshal([]byte(catsJSON), &cats)
		return cats
	}
	return nil
}

// ymViewCount returns view count from ym or falls back to clip DB metadata.
func ymViewCount(ym *DownloaderMetadata, clip *asset.Asset) int64 {
	if ym != nil {
		return ym.ViewCount
	}
	countStr := clip.GetMetadataString("youtube_view_count")
	if countStr != "" {
		if n, err := strconv.ParseInt(countStr, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// ymUploadDate returns upload date from ym or falls back to clip DB metadata.
func ymUploadDate(ym *DownloaderMetadata, clip *asset.Asset) string {
	if ym != nil && ym.UploadDate != "" {
		return ym.UploadDate
	}
	return clip.GetMetadataString("youtube_upload_date")
}

// ymThumbnailURL returns the thumbnail URL from ym or falls back to clip DB metadata.
func ymThumbnailURL(ym *DownloaderMetadata, clip *asset.Asset) string {
	if ym != nil && ym.ThumbnailURL != "" {
		return ym.ThumbnailURL
	}
	return clip.GetMetadataString("youtube_thumbnail")
}



// metadataStringSlice extracts a []string from a metadata map, accepting
// []string, []any, or JSON-encoded string values.
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
		return tagutil.NormalizeClipTagList(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return tagutil.NormalizeClipTagList(out)
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		var out []string
		if err := json.Unmarshal([]byte(v), &out); err == nil {
			return tagutil.NormalizeClipTagList(out)
		}
	}
	return nil
}

// metadataFloat64 extracts a float64 from a metadata map, accepting
// float64, float32, int, int64, json.Number, or numeric string values.
func metadataFloat64(meta map[string]any, key string) float64 {
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
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return 0
}

// metadataBool extracts a bool from a metadata map, accepting bool or
// "true"/"false" string values.
func metadataBool(meta map[string]any, key string) bool {
	if meta == nil {
		return false
	}
	raw, ok := meta[key]
	if !ok || raw == nil {
		return false
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}

// metadataInt extracts an int from a metadata map, accepting int, int32,
// int64, float64, json.Number, or numeric string values.
func metadataInt(meta map[string]any, key string) int {
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


