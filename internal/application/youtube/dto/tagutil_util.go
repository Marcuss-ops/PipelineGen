package dto

import (
	"crypto/md5"
	"fmt"
	"net/url"
	"os"
	"strings"

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

// FallbackMD5String computes an MD5 hex digest of a string.
func FallbackMD5String(data string) string {
	h := md5.Sum([]byte(data))
	return fmt.Sprintf("%x", h)
}

// FallbackMD5File computes an MD5 hex digest of a file's contents.
func FallbackMD5File(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	h := md5.Sum(data)
	return fmt.Sprintf("%x", h)
}

// IsTransientDownloadError returns true if the error is likely transient
// and worth retrying (e.g. timeout, connection reset, HTTP 429/5xx).
// Permanent errors (video unavailable, private, invalid URL, etc.) return false.
func IsTransientDownloadError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	permanentPatterns := []string{
		"video unavailable", "private video", "sign in to confirm",
		"confirm your age", "requested format is not available",
		"invalid url", "unable to extract", "no video formats", "video is live",
	}
	for _, p := range permanentPatterns {
		if strings.Contains(errStr, p) {
			return false
		}
	}

	transientPatterns := []string{
		"timeout", "connection reset", "connection refused",
		"temporary failure", "fragment download failed",
		"no route to host", "network is unreachable",
		"i/o timeout", "broken pipe",
	}
	for _, p := range transientPatterns {
		if strings.Contains(errStr, p) {
			return true
		}
	}

	if strings.Contains(errStr, "http 429") || strings.Contains(errStr, "http 5") {
		return true
	}

	return false
}

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
