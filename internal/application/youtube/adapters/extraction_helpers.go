package adapters

import (
	"regexp"

	"strings"

	tagutil "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Regex patterns (from extractor_clean.go) ─────────────────────────────

var (
	timestampRegex        = regexp.MustCompile(`(?m)^\s*\d{1,2}:\d{2}(?::\d{2})?\s*$`)
	urlRegex              = regexp.MustCompile(`https?://\S+|www\.\S+`)
	socialPatterns        = regexp.MustCompile(`(?i)(?m)^\s*(subscribe|like|comment|share|follow|join|hit the bell|notification|click the link|check out|sign up|use code|promo code|discount|affiliate|sponsored|ad\b|advertisement|merch|merchandise|store|shop|buy|purchase|deal|offer|coupon)\b.*$`)
	sponsorPatterns       = regexp.MustCompile(`(?i)(?m)^\s*(use code|promo code|discount code|affiliate|sponsored by|brought to you by|partner with|thanks to|special thanks|shoutout|shout out)\b.*$`)
	timestampContextRegex = regexp.MustCompile(`(?m)^\s*\d{1,2}:\d{2}(?::\d{2})?\s*[-–—]?\s*.+$`)
	emojiRegex            = regexp.MustCompile(`[\x{1F600}-\x{1F64F}\x{1F300}-\x{1F5FF}\x{1F680}-\x{1F6FF}\x{1F1E0}-\x{1F1FF}\x{2702}-\x{27B0}\x{24C2}-\x{1F251}]`)
	excessWhitespaceRegex = regexp.MustCompile(`\n{3,}`)
)

// ── YouTube video ID extraction (from extractor_process.go) ─────────────

// extractYouTubeVideoID extracts the YouTube video ID from a clip ID or clip metadata.
func extractYouTubeVideoID(clipID string, existing *asset.Asset) string {
	if existing != nil {
		vid := existing.YouTubeVideoID()
		if vid != "" {
			return vid
		}
		url := existing.YouTubeURL()
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
	parts := strings.Split(clipID, "_")
	if len(parts) >= 3 && parts[0] == "yt" {
		return parts[1]
	}
	return ""
}

// ── URL helpers (from util.go) ───────────────────────────────────────────

func canonicalYouTubeURL(inputURL, videoID string) string {
	return tagutil.CanonicalYouTubeURL(inputURL, videoID)
}

func validateDownloadURL(rawURL string) error {
	return tagutil.ValidateDownloadURL(rawURL)
}
