package urlutil

import (
	"fmt"
	"net/url"
	"strings"
)

// ExtractVideoID extracts the video ID from a YouTube URL.
// Supports youtu.be, youtube.com/watch, /shorts/, /embed/, /live/,
// and mobile (m.youtube.com) variants.
func ExtractVideoID(rawURL string) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("empty URL")
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	// Handle youtu.be short links
	if parsed.Hostname() == "youtu.be" {
		path := strings.TrimPrefix(parsed.Path, "/")
		if path != "" {
			if idx := strings.Index(path, "?"); idx != -1 {
				path = path[:idx]
			}
			return path, nil
		}
	}

	// Handle youtube.com URLs (including m.youtube.com, www.youtube.com, etc.)
	if strings.Contains(parsed.Hostname(), "youtube.com") {
		// Standard watch URLs: youtube.com/watch?v=ID
		if parsed.Path == "/watch" {
			v := parsed.Query().Get("v")
			if v == "" {
				return "", fmt.Errorf("no video ID in watch URL")
			}
			return v, nil
		}
		// Shorts URLs: youtube.com/shorts/ID
		if strings.HasPrefix(parsed.Path, "/shorts/") {
			id := strings.TrimPrefix(parsed.Path, "/shorts/")
			if idx := strings.Index(id, "?"); idx != -1 {
				id = id[:idx]
			}
			return id, nil
		}
		// Embed URLs: youtube.com/embed/ID
		if strings.HasPrefix(parsed.Path, "/embed/") {
			id := strings.TrimPrefix(parsed.Path, "/embed/")
			if idx := strings.Index(id, "?"); idx != -1 {
				id = id[:idx]
			}
			return id, nil
		}
		// Live URLs: youtube.com/live/ID
		if strings.HasPrefix(parsed.Path, "/live/") {
			id := strings.TrimPrefix(parsed.Path, "/live/")
			if idx := strings.Index(id, "?"); idx != -1 {
				id = id[:idx]
			}
			return id, nil
		}
		return "", fmt.Errorf("unrecognized youtube.com URL path: %s", parsed.Path)
	}

	return "", fmt.Errorf("not a YouTube URL")
}
