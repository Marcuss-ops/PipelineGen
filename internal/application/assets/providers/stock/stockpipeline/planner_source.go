package stockpipeline

import (
	"net/url"
	"strings"

	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// inferSourceProvider classifies a source URL into the canonical
// 4-bucket provider taxonomy. Single canonical implementer per
// godlike/06 SSOT — every consumer reads plan.SourceProvider which
// is set ONCE at plan-build time.
//
// godlike/07 NO-FAKE-AVAILABILITY: parsing the hostname (not
// substring-matching the full URL) closes the SSRF-style false-
// positive class — a URL like
// `https://fake-youtube.com.attacker.io/video.mp4` would have
// matched the substring-based classification. We delegate to
// net/url.Parse + Hostname() + suffix/exact-host match so the
// only counts are on real YouTube/Pexels/Pixabay domains. Bare
// non-URL inputs (parse error OR empty host) collapse to
// SourceProviderUnknown — the bucket is observable and never
// silent-empty.
func inferSourceProvider(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return SourceProviderUnknown
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return SourceProviderUnknown
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return SourceProviderUnknown
	}
	switch {
	case host == "youtu.be",
		host == "youtube.com",
		strings.HasSuffix(host, ".youtube.com"):
		return SourceProviderYouTube
	case host == "pexels.com", strings.HasSuffix(host, ".pexels.com"):
		return SourceProviderPexels
	case host == "pixabay.com", strings.HasSuffix(host, ".pixabay.com"):
		return SourceProviderPixabay
	}
	return SourceProviderUnknown
}

// inferSourceVideoID extracts the canonical video ID ONLY when
// the URL belongs to the YouTube provider bucket. Returns "" for
// non-YouTube URLs (provider mismatch — caller can rely on
// plan.SourceProvider too) AND for malformed YouTube URLs where
// ExtractVideoID errors (channel pages, playlists, bare /UCxxx).
//
// godlike/07 fail-open rationale: SourceVideoID is an
// observability field (not a gate). Failing the entire chunk
// build because a YouTube channel-page URL has no watch-ID would
// surface false-positive FAILED jobs for an otherwise valid
// SourceURL. Callers that NEED the ID can re-run the extraction
// verbatim — the function is pure + deterministic.
func inferSourceVideoID(rawURL string) string {
	vid, err := urlutil.ExtractVideoID(rawURL)
	if err != nil {
		return ""
	}
	return vid
}
