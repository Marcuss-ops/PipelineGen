package youtube

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/assets"
)

// extractYouTubeVideoID extracts the YouTube video ID from a clip ID (yt_{videoID}_*) or clip metadata.
func extractYouTubeVideoID(clipID string, existing *assets.Asset) string {
	// Try from metadata first (most reliable)
	if existing != nil {
		vid := existing.GetMetadataString("youtube_video_id")
		if vid != "" {
			return vid
		}
		// Try from URL
		url := existing.GetMetadataString("youtube_url")
		if url != "" {
			if strings.Contains(url, "youtube.com/watch?v=") {
				parts := strings.Split(url, "v=")
				if len(parts) > 1 {
					id := strings.Split(parts[1], "&")[0]
					return id
				}
			}
		}
	}
	// Fallback: extract from clip ID format "yt_{videoID}_{start}_{end}"
	parts := strings.Split(clipID, "_")
	if len(parts) >= 3 && parts[0] == "yt" {
		return parts[1]
	}
	return ""
}
