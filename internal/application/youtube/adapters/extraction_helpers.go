package adapters

import (
	tagutil "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

// ── URL helpers (from util.go) ───────────────────────────────────────────

func canonicalYouTubeURL(inputURL, videoID string) string {
	return tagutil.CanonicalYouTubeURL(inputURL, videoID)
}
