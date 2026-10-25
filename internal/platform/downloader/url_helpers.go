// Package downloader — url_helpers.go: pure URL classification helpers.
//
// These helpers classify a source URL by its SHAPE alone; they perform no I/O
// (no HTTP probe, no headless browser), so a caller that needs a real
// availability check must still download.
//
// HISTORY (September 2026): IsDirectMediaURL / IsHLSURL previously lived in
// capability was demolished they moved here, next to the generic HTTP/yt-dlp
// downloaders that actually consume them — the media processor
package downloader

import "strings"

// IsDirectMediaURL reports whether the URL points to a direct progressive media
// file (a container this tree can fetch over plain HTTP without remuxing).
func IsDirectMediaURL(url string) bool {
	u := strings.ToLower(strings.TrimSpace(url))
	return strings.HasSuffix(u, ".mp4") || strings.HasSuffix(u, ".mov") || strings.HasSuffix(u, ".avi")
}

// IsHLSURL reports whether the URL points to an HLS playlist, which must be
// remuxed (FFmpeg) rather than fetched as a progressive file.
func IsHLSURL(url string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(url)), ".m3u8")
}
