package dto

import (
	"net/url"
	"strings"
)

// CanonicalYouTubeURL normalizes a YouTube URL to the standard watch format.
func CanonicalYouTubeURL(inputURL, videoID string) string {
	if videoID == "" {
		return ""
	}
	parsed, err := url.Parse(inputURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return ""
	}
	if strings.Contains(host, "youtube.com") || host == "youtu.be" {
		return "https://www.youtube.com/watch?v=" + videoID
	}
	return ""
}

// ValidateDownloadURL validates that a URL is from an allowed host.

// FallbackMD5String returns the MD5 hex digest of a string, delegating to
// the canonical checksum package (compat-only — never identity/dedup).

// FallbackMD5File returns the MD5 hex digest of a file's contents,
// delegating to the streaming checksum package (compat-only).

// IsTransientDownloadError was removed in Azione 2/8 of Step 7 (July 2026):
// migrated to pkg/retry.IsTransient. Callers that previously used
// tagutil.IsTransientDownloadError should now use retry.IsTransient directly.
// The permanent-pattern block (video unavailable, private video, etc.) is
// preserved implicitly — none of those substrings match the canonical
// transient taxonomy, so retry.IsTransient correctly returns false for them.
// See pkg/retry/retry.go for the canonical transient substring taxonomy.
//
// For YouTube-specific permanent errors that happen to contain transient
// substrings (a rare cross-cutting case), wrap with
// &retry.TransientInfrastructureError{} to override, OR add the substring
// to pkg/retry.transientSubstrings via a focused PR + test.

// TokenSetForText builds a token set from raw text by lowercasing, cleaning,
// splitting, and filtering short/generic tokens.

// TokenSetFromStrings aggregates token sets from multiple string slices.

// TextJaccardScore returns the Jaccard similarity of two texts after tokenization.

// SliceJaccardScore returns the Jaccard similarity of two string slices
// after tokenization.

// MergeStringSlices merges multiple string slices, normalizing each item
// via NormalizeSemanticText and deduplicating.
