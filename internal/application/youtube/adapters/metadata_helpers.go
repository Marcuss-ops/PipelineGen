package adapters

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
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

// ── Sponsor detection ─────────────────────────────────────────────────────

var sponsorKeywords = []string{
	"use code", "promo code", "discount code", "affiliate",
	"sponsored by", "brought to you by", "partner with",
	"thanks to", "special thanks", "shoutout",
	"check out", "sign up", "click the link",
	"merch", "store", "shop", "buy", "purchase",
	"deal", "offer", "coupon",
	"bluechew", "celsius", "tecovas", "perplexity",
	"expressvpn", "nordvpn", "surfshark", "raidsafe",
	"skillshare", "audible", "helix sleep", "squarespace",
	"freshly", "hello fresh", "factor", "manscaped",
	"warby parker", "third love", "bombas",
}

func isSponsorSegment(text string) bool {
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	for _, keyword := range sponsorKeywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

// ── Quality scoring ───────────────────────────────────────────────────────

func calculateQualityScore(transcript, title, description string, tags []string, duration float64, meta *dto.CanonicalClipMetadata) float64 {
	heuristic := calculateHeuristicQualityScore(transcript, title, description, tags, duration, meta)
	if meta != nil && meta.QualityScore > 0 {
		score := (heuristic * 0.72) + (meta.QualityScore * 0.28)
		if heuristic >= 0.82 && score < 0.78 {
			score = heuristic * 0.90
		}
		if meta.QualityScore >= 0.80 && score < 0.80 {
			score = 0.80
		}
		if heuristic < 0.35 && score > heuristic {
			score = heuristic
		}
		if score < 0 {
			score = 0
		}
		if score > 1.0 {
			score = 1.0
		}
		return score
	}
	return heuristic
}

func calculateHeuristicQualityScore(transcript, title, description string, tags []string, duration float64, meta *dto.CanonicalClipMetadata) float64 {
	score := 0.08
	transcriptLen := len(transcript)
	switch {
	case transcriptLen >= 1200:
		score += 0.18
	case transcriptLen >= 700:
		score += 0.14
	case transcriptLen >= 300:
		score += 0.10
	case transcriptLen >= 120:
		score += 0.06
	case transcriptLen > 0:
		score += 0.02
	}
	if title != "" {
		score += 0.03
		if len(title) > 20 {
			score += 0.03
		}
		if len(title) > 40 {
			score += 0.02
		}
	}
	switch {
	case len(tags) >= 5:
		score += 0.04
	case len(tags) >= 2:
		score += 0.03
	case len(tags) == 1:
		score += 0.01
	}
	switch {
	case duration >= 25 && duration <= 180:
		score += 0.16
	case duration >= 12 && duration <= 300:
		score += 0.08
	case duration >= 8 && duration <= 600:
		score += 0.03
	default:
		score -= 0.10
	}
	if transcriptLen < 200 {
		score -= 0.03
	}
	if duration < 20 {
		score -= 0.10
	}
	if duration < 15 {
		score -= 0.05
	}
	if duration > 240 {
		score -= 0.05
	}
	if meta != nil {
		if meta.Summary != "" {
			score += 0.10
		}
		if meta.Hook != "" {
			score += 0.10
		}
		if meta.CleanTitle != "" && tagutil.NormalizeClipTag(meta.CleanTitle) != tagutil.NormalizeClipTag(title) {
			score += 0.06
		}
		switch {
		case len(meta.Topics) >= 5:
			score += 0.12
		case len(meta.Topics) >= 3:
			score += 0.10
		case len(meta.Topics) >= 2:
			score += 0.07
		case len(meta.Topics) == 1:
			score += 0.03
		}
		switch {
		case len(meta.Speakers) >= 2:
			score += 0.06
		case len(meta.Speakers) == 1:
			score += 0.03
		}
		switch {
		case len(meta.MentionedPeople) >= 2:
			score += 0.05
		case len(meta.MentionedPeople) == 1:
			score += 0.03
		}
		if len(meta.SourceTags) > 0 {
			score += 0.02
		}
		if len(meta.ClipTags) > 0 {
			score += 0.03
		}
		if len(meta.SearchKeywords) > 0 {
			score += 0.03
		}
		if len(meta.CleanTranscript) > 100 {
			score += 0.05
		}
		if len(meta.EmbeddingText) > 300 {
			score += 0.02
		}
		if duration >= 25 && duration <= 180 && meta.Summary != "" && meta.Hook != "" && len(meta.Topics) >= 3 {
			score += 0.18
		}
		if duration >= 45 && duration <= 180 && len(meta.MentionedPeople) >= 1 && len(meta.SearchKeywords) >= 2 {
			score += 0.08
		}
		if duration >= 45 && duration <= 180 && meta.Summary != "" && meta.Hook != "" && len(meta.Topics) >= 3 && len(meta.Speakers) >= 1 && score < 0.72 {
			score = 0.72
		}
	}
	if isSponsorSegment(transcript) {
		score -= 0.18
	}
	if score < 0 {
		score = 0
	}
	if score > 1.0 {
		score = 1.0
	}
	return score
}

func getQualityTier(score float64) string {
	switch {
	case score >= 0.80:
		return "high"
	case score >= 0.55:
		return "medium"
	case score >= 0.30:
		return "low"
	default:
		return "poor"
	}
}
