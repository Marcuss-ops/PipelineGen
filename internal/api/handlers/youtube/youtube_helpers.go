package youtube

import (
	"regexp"
	"strings"
)

// resolveChannelURL constructs a YouTube channel URL from various inputs.
func resolveChannelURL(channelURL, channelID, channelName string) string {
	if channelURL != "" {
		// Handle @username format
		if strings.Contains(channelURL, "@") && strings.Contains(channelURL, "youtube.com") {
			re := regexp.MustCompile(`@(\w+)`)
			match := re.FindStringSubmatch(channelURL)
			if len(match) > 1 {
				return "https://www.youtube.com/@" + match[1] + "/videos"
			}
		}
		if strings.Contains(channelURL, "/videos") {
			return channelURL
		}
		return channelURL + "/videos"
	}
	if channelID != "" {
		return "https://www.youtube.com/channel/" + channelID + "/videos"
	}
	if channelName != "" {
		return "https://www.youtube.com/@" + channelName + "/videos"
	}
	return ""
}

// extractVideoID extracts the video ID from a YouTube URL.
func extractVideoID(url string) string {
	patterns := []string{
		`(?:youtube\.com/watch\?v=)([a-zA-Z0-9_-]{11})`,
		`(?:youtu\.be/)([a-zA-Z0-9_-]{11})`,
		`(?:youtube\.com/embed/)([a-zA-Z0-9_-]{11})`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		match := re.FindStringSubmatch(url)
		if len(match) > 1 {
			return match[1]
		}
	}
	return ""
}
