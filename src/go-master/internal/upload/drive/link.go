package drive

import (
	"net/url"
	"strings"
)

// FileIDFromLink extracts a Google Drive file ID from various URL formats.
// Supports: /file/d/ID, ?id=ID, /open?id=ID
func FileIDFromLink(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// Try parsing as URL
	if u, err := url.Parse(raw); err == nil {
		// Check for id parameter (?id=FILE_ID)
		if id := strings.TrimSpace(u.Query().Get("id")); id != "" {
			return id
		}

		// Check path: /file/d/FILE_ID or /open?id=FILE_ID
		path := strings.Trim(u.Path, "/")
		parts := strings.Split(path, "/")
		for i := 0; i < len(parts)-1; i++ {
			if parts[i] == "d" || parts[i] == "file" {
				return parts[i+1]
			}
		}
	}

	// Fallback: look for id= in string
	if idx := strings.Index(raw, "id="); idx >= 0 {
		id := raw[idx+3:]
		if cut := strings.IndexAny(id, "&?#"); cut >= 0 {
			id = id[:cut]
		}
		return id
	}

	return ""
}
