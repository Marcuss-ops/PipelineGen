// Package ingest owns pure source-cache identity rules for stock ingestion.
// It has no filesystem, database, downloader, or orchestration dependency.
package ingest

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// DeriveSourceCacheKey computes the deterministic cache identity for a source
// and its download parameters.
func DeriveSourceCacheKey(rawURL, downloadSection, mergeFormat string, forceKeyframes bool) string {
	canon := NormalizeSourceURL(rawURL)
	force := "false"
	if forceKeyframes {
		force = "true"
	}
	input := fmt.Sprintf("stock-source:%s|%s|%s|%s", canon, downloadSection, mergeFormat, force)
	hash := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", hash)
}

// NormalizeSourceURL canonicalizes supported YouTube URLs to the watch form.
// Non-YouTube URLs and URLs without an extractable ID remain unchanged.
func NormalizeSourceURL(raw string) string {
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)
	if !strings.Contains(lower, "youtube.com") && !strings.Contains(lower, "youtu.be") {
		return raw
	}
	if id := ExtractVideoID(raw); id != "" {
		return "https://www.youtube.com/watch?v=" + id
	}
	return raw
}

// ExtractVideoID extracts the supported YouTube video ID forms used by the
// stock source cache. It deliberately preserves the existing permissive
// behavior for compatibility with stored cache keys.
func ExtractVideoID(raw string) string {
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)

	if strings.Contains(lower, "youtu.be/") {
		if idx := strings.LastIndex(raw, "/"); idx >= 0 {
			id := strings.TrimSpace(raw[idx+1:])
			if id != "" {
				return id
			}
		}
	}

	if strings.Contains(lower, "youtube.com") {
		if idx := strings.Index(lower, "v="); idx >= 0 {
			rest := raw[idx+2:]
			if ampIdx := strings.IndexAny(rest, "&# \t\n"); ampIdx > 0 {
				return strings.TrimSpace(rest[:ampIdx])
			}
			return strings.TrimSpace(rest)
		}
	}
	return ""
}
