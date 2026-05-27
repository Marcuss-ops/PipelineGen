// Package mediautil provides shared media utility functions.
package mediautil

import (
	"net/url"
	"strings"
)

// ExtractVideoID extracts the video ID from a YouTube URL.
// Supports youtu.be, /watch?v=, /shorts/, /embed/, /live/ formats.
func ExtractVideoID(inputURL string) string {
	parsed, err := url.Parse(inputURL)
	if err != nil {
		return ""
	}

	if parsed.Hostname() == "youtu.be" {
		path := strings.TrimPrefix(parsed.Path, "/")
		if path != "" {
			return path
		}
	}

	if strings.Contains(parsed.Hostname(), "youtube.com") {
		if parsed.Path == "/watch" {
			return parsed.Query().Get("v")
		}
		if strings.HasPrefix(parsed.Path, "/shorts/") {
			id := strings.TrimPrefix(parsed.Path, "/shorts/")
			if idx := strings.Index(id, "?"); idx != -1 {
				id = id[:idx]
			}
			return id
		}
		if strings.HasPrefix(parsed.Path, "/embed/") {
			id := strings.TrimPrefix(parsed.Path, "/embed/")
			if idx := strings.Index(id, "?"); idx != -1 {
				id = id[:idx]
			}
			return id
		}
		if strings.HasPrefix(parsed.Path, "/live/") {
			id := strings.TrimPrefix(parsed.Path, "/live/")
			if idx := strings.Index(id, "?"); idx != -1 {
				id = id[:idx]
			}
			return id
		}
	}

	return ""
}
