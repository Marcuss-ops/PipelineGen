package youtube

import "strings"

// extractIDFromURL extracts the video ID from standard YouTube watch and
// short URLs. It is kept local to the subtitle adapter package so the
// platform adapter does not depend on an unrelated infrastructure package.
func extractIDFromURL(rawURL string) string {
	if strings.Contains(rawURL, "youtube.com/watch") {
		for _, part := range strings.Split(rawURL, "&") {
			if strings.HasPrefix(part, "v=") || strings.Contains(part, "?v=") {
				if idx := strings.Index(part, "v="); idx >= 0 {
					id := part[idx+2:]
					if len(id) > 11 {
						id = id[:11]
					}
					return id
				}
			}
		}
	}
	if idx := strings.LastIndex(rawURL, "/"); idx >= 0 {
		return rawURL[idx+1:]
	}
	return rawURL
}
