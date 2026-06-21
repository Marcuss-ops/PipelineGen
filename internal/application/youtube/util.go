package youtube

import (
	"crypto/md5"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// getGroupFromDestination extracts group name from destination request
func getGroupFromDestination(dest *DestinationRequest) string {
	if dest == nil {
		return ""
	}
	return dest.Group
}

// canonicalYouTubeURL normalizes a YouTube URL to the standard watch format.
func canonicalYouTubeURL(inputURL, videoID string) string {
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

// validateDownloadURL validates that a URL is from an allowed host.
// This replaces the infrastructure/security.ValidateDownloadURL import.
func validateDownloadURL(rawURL string) error {
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

// fallbackMD5String computes an MD5 hex string without importing infrastructure packages.
func fallbackMD5String(data string) string {
	h := md5.Sum([]byte(data))
	return fmt.Sprintf("%x", h)
}

// fallbackMD5File computes an MD5 hex string of a file's contents.
func fallbackMD5File(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	h := md5.Sum(data)
	return fmt.Sprintf("%x", h)
}
