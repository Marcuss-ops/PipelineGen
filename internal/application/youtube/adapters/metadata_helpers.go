package adapters

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	tagutil "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	ports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ── YouTube metadata field accessors ──────────────────────────────────────

func ymDescription(ym *ports.DownloaderMetadata, clip *asset.Asset) string {
	if ym != nil && ym.Description != "" {
		return tagutil.CompactYouTubeDescription(ym.Description)
	}
	desc := clip.GetMetadataString("youtube_description")
	if desc != "" {
		return tagutil.CompactYouTubeDescription(desc)
	}
	return ""
}

func ymTags(ym *ports.DownloaderMetadata, clip *asset.Asset) []string {
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
	if len(clip.Tags) > 0 {
		return tagutil.NormalizeClipTagList(clip.Tags)
	}
	return nil
}

func ymCategories(ym *ports.DownloaderMetadata, clip *asset.Asset) []string {
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

func ymViewCount(ym *ports.DownloaderMetadata, clip *asset.Asset) int64 {
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

func ymUploadDate(ym *ports.DownloaderMetadata, clip *asset.Asset) string {
	if ym != nil && ym.UploadDate != "" {
		return ym.UploadDate
	}
	return clip.GetMetadataString("youtube_upload_date")
}

func ymThumbnailURL(ym *ports.DownloaderMetadata, clip *asset.Asset) string {
	if ym != nil && ym.ThumbnailURL != "" {
		return ym.ThumbnailURL
	}
	return clip.GetMetadataString("youtube_thumbnail")
}

// ── Metadata map accessors ────────────────────────────────────────────────

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

// ── Clip timestamp parsing ────────────────────────────────────────────────

func parseClipTimestamps(clipID string) (startSec, endSec int) {
	parts := strings.Split(clipID, "_")
	if len(parts) >= 4 && parts[0] == "yt" {
		if s, err := strconv.Atoi(parts[len(parts)-2]); err == nil {
			startSec = s
		}
		if e, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
			endSec = e
		}
	}
	return
}

// buildVideoURL constructs a YouTube URL from a clip's metadata or ID.
func buildVideoURL(clipID string, existing *asset.Asset) string {
	videoURL := existing.GetMetadataString("youtube_url")
	if videoURL != "" {
		return videoURL
	}
	videoID := existing.GetMetadataString("youtube_video_id")
	if videoID != "" {
		return fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
	}
	parts := strings.Split(clipID, "_")
	if len(parts) >= 3 && parts[0] == "yt" {
		return fmt.Sprintf("https://www.youtube.com/watch?v=%s", parts[1])
	}
	return ""
}

