// Package downloader — resolver_url_helpers.go: pure URL classification
// helpers (Step 3 follow-up, July 2026).
//
// godlike/06 SSOT: the URL classification helpers (IsArtlistURL /
// IsDirectMediaURL / IsHLSURL) are the SINGLE canonical surface for
// "what kind of Artlist URL is this?". Before this split they lived
// inside the Resolver file alongside the routing logic; that
// co-location was a godlike/06 violation because the helpers are
// callable from any package — notably
// internal/platform/media/processor/processor_download.go uses
// IsArtlistURL / IsDirectMediaURL / IsHLSURL in its own routing
// decision. Separating them here documents the cross-package surface
// explicitly and keeps the canonical Resolver type small.
//
// godlike/07 NO-FAKE-AVAILABILITY: these helpers return the canonical
// boolean verdict based on URL shape; they do NOT perform any I/O
// (no HTTP probe, no Playwright, no headless browser). Callers that
// need a real availability check use the Node scraper /download path
// (see resolver_scraper.go::downloadViaScraper) or the canonical
// Resolver.Download entry point (see resolver.go).
package downloader

import "strings"

// IsArtlistURL checks if the URL is from Artlist's CDN.
// Exported for use by the media processor's downloadStep fallback path
// (PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY-CUTOVER, July 2026).
func IsArtlistURL(url string) bool {
	u := strings.ToLower(strings.TrimSpace(url))
	return strings.Contains(u, "artlist") || strings.Contains(u, "cdn.artlist")
}

// IsDirectMediaURL checks if the URL points to a direct progressive media file.
func IsDirectMediaURL(url string) bool {
	u := strings.ToLower(strings.TrimSpace(url))
	return strings.HasSuffix(u, ".mp4") || strings.HasSuffix(u, ".mov") || strings.HasSuffix(u, ".avi")
}

// IsHLSURL checks if the URL points to an HLS playlist.
func IsHLSURL(url string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(url)), ".m3u8")
}
