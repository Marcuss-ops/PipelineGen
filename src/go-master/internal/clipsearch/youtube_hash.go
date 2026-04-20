package clipsearch

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
)

func buildYouTubeInterviewHash(meta *YouTubeClipMetadata) string {
	if meta == nil {
		return ""
	}
	videoID := strings.TrimSpace(strings.ToLower(meta.VideoID))
	if videoID == "" {
		return ""
	}
	payload := videoID
	if meta.SelectedMoment != nil {
		// Round to seconds so tiny float differences do not generate distinct hashes.
		payload = fmt.Sprintf("%s|%.0f|%.0f", videoID, meta.SelectedMoment.StartSec, meta.SelectedMoment.EndSec)
	}
	sum := sha1.Sum([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func containsTag(tags []string, expected string) bool {
	want := strings.TrimSpace(strings.ToLower(expected))
	if want == "" {
		return false
	}
	for _, t := range tags {
		if strings.TrimSpace(strings.ToLower(t)) == want {
			return true
		}
	}
	return false
}
