package dto

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/checksum"
	similarity "github.com/Marcuss-ops/PipelineGen/pkg/similarity"
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
func ValidateDownloadURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("URL is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	host := strings.ToLower(parsed.Hostname())
	allowed := []string{
		"youtube.com", "www.youtube.com", "youtu.be",
		"m.youtube.com",
	}
	for _, a := range allowed {
		if host == a || strings.HasSuffix(host, "."+a) {
			return nil
		}
	}
	return fmt.Errorf("URL host %q is not in the allowed list", host)
}

// FallbackMD5String returns the MD5 hex digest of a string, delegating to
// the canonical checksum package (compat-only — never identity/dedup).
func FallbackMD5String(data string) string {
	return checksum.LegacyMD5String(data)
}

// FallbackMD5File returns the MD5 hex digest of a file's contents,
// delegating to the streaming checksum package (compat-only).
func FallbackMD5File(path string) string {
	h, err := checksum.LegacyMD5File(path)
	if err != nil {
		return ""
	}
	return h
}

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
func TokenSetForText(text string) map[string]struct{} {
	text = strings.ToLower(text)
	text = CleanYouTubeDescription(text)
	text = CleanClipTranscript(text)
	replacer := strings.NewReplacer(
		",", " ", ".", " ", "!", " ", "?", " ", ";", " ", ":", " ",
		"(", " ", ")", " ", "[", " ", "]", " ", "-", " ", "_", " ",
		"\"", " ", "'", " ", "/", " ", "\\", " ",
		"&", " ", "|", " ", "#", " ",
	)
	text = replacer.Replace(text)
	set := make(map[string]struct{})
	for _, word := range strings.Fields(text) {
		word = strings.TrimSpace(word)
		if len(word) < 3 {
			continue
		}
		if IsGenericToken(word) {
			continue
		}
		set[word] = struct{}{}
	}
	return set
}

// TokenSetFromStrings aggregates token sets from multiple string slices.
func TokenSetFromStrings(values ...[]string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, list := range values {
		for _, item := range list {
			for tok := range TokenSetForText(item) {
				set[tok] = struct{}{}
			}
		}
	}
	return set
}

// TextJaccardScore returns the Jaccard similarity of two texts after tokenization.
func TextJaccardScore(a, b string) float64 {
	return similarity.Jaccard(TokenSetForText(a), TokenSetForText(b))
}

// SliceJaccardScore returns the Jaccard similarity of two string slices
// after tokenization.
func SliceJaccardScore(a, b []string) float64 {
	return similarity.Jaccard(TokenSetFromStrings(a), TokenSetFromStrings(b))
}

// MergeStringSlices merges multiple string slices, normalizing each item
// via NormalizeSemanticText and deduplicating.
func MergeStringSlices(values ...[]string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, list := range values {
		for _, item := range list {
			norm := NormalizeSemanticText(item)
			if norm == "" {
				continue
			}
			if _, ok := seen[norm]; ok {
				continue
			}
			seen[norm] = struct{}{}
			out = append(out, norm)
		}
	}
	return out
}
