// Package youtube — leaf forwarding wrappers to tagutil (CPR-CC-6 split, June 2026).
package youtube

import (
	tagutil "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/tagutil"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/types"
)

// getGroupFromDestination extracts group name from destination request.
func getGroupFromDestination(dest *youtubetypes.DestinationRequest) string {
	if dest == nil {
		return ""
	}
	return dest.Group
}

// canonicalYouTubeURL delegates to the canonical implementation in tagutil.
func canonicalYouTubeURL(inputURL, videoID string) string {
	return tagutil.CanonicalYouTubeURL(inputURL, videoID)
}

// validateDownloadURL delegates to the canonical implementation in tagutil.
func validateDownloadURL(rawURL string) error {
	return tagutil.ValidateDownloadURL(rawURL)
}

// fallbackMD5String delegates to the canonical implementation in tagutil.
func fallbackMD5String(data string) string {
	return tagutil.FallbackMD5String(data)
}

// fallbackMD5File delegates to the canonical implementation in tagutil.
func fallbackMD5File(path string) string {
	return tagutil.FallbackMD5File(path)
}
