package security

import (
	"fmt"
	"net/url"
	"strings"
)

// allowedHosts defines domains permitted for yt-dlp downloads.
var allowedHosts = map[string]bool{
	// Video platforms (yt-dlp)
	"youtube.com":       true,
	"www.youtube.com":   true,
	"youtu.be":          true,
	"m.youtube.com":     true,
	"tiktok.com":        true,
	"www.tiktok.com":    true,
	"vm.tiktok.com":     true,
	"instagram.com":     true,
	"www.instagram.com": true,
	"twitter.com":       true,
	"x.com":             true,
	"www.x.com":         true,
	"vimeo.com":         true,
	"dailymotion.com":   true,
	"www.dailymotion.com": true,
	"twitch.tv":         true,
	"www.twitch.tv":     true,
	"facebook.com":      true,
	"www.facebook.com":  true,
	// Artlist CDN / stock platforms
	"artlist.com":       true,
	"www.artlist.com":   true,
	"cdn.artlist.io":    true,
	"artlist-cdn.s3.amazonaws.com": true,
	// Generic HTTP (for Artlist proxied URLs and other stock sources)
	"s3.amazonaws.com":  true,
	"storage.googleapis.com": true,
}

// ValidateDownloadURL checks that a URL is safe to pass to yt-dlp or similar tools.
// It ensures the URL has an allowed scheme (http/https), a valid host, and no
// shell-injection characters.
func ValidateDownloadURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("url is empty")
	}

	// Reject obvious injection attempts before parsing
	if strings.ContainsAny(rawURL, ";|&$`\\\"'!<>") {
		return fmt.Errorf("url contains forbidden characters")
	}
	if strings.HasPrefix(rawURL, "-") {
		return fmt.Errorf("url cannot start with a dash (flag injection)")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}

	// Only allow http/https schemes
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported url scheme %q (only http/https allowed)", u.Scheme)
	}

	// Validate host against allowlist
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("url has no host")
	}
	if !isAllowedHost(host) {
		return fmt.Errorf("host %q is not in the allowed list", host)
	}

	// Reject userinfo (e.g. http://evil@youtube.com)
	if u.User != nil {
		return fmt.Errorf("url must not contain userinfo")
	}

	// Reject fragment to prevent weird behavior
	if u.Fragment != "" {
		return fmt.Errorf("url must not contain a fragment")
	}

	return nil
}

// isAllowedHost checks whether the host (or its parent domain) is in the allowlist.
func isAllowedHost(host string) bool {
	if allowedHosts[host] {
		return true
	}
	// Check parent domain: e.g., "sub.youtube.com" → "youtube.com"
	parts := strings.Split(host, ".")
	for i := 1; i < len(parts); i++ {
		parent := strings.Join(parts[i:], ".")
		if allowedHosts[parent] {
			return true
		}
	}
	return false
}

// SanitizeTimestamp validates a timestamp string (e.g. "01:23" or "1:23:45")
// to prevent injection when passed as a flag argument to ffmpeg/yt-dlp.
func SanitizeTimestamp(ts string) error {
	if ts == "" {
		return fmt.Errorf("timestamp is empty")
	}
	if strings.HasPrefix(ts, "-") {
		return fmt.Errorf("timestamp cannot start with dash")
	}
	for _, c := range ts {
		if !((c >= '0' && c <= '9') || c == ':' || c == '.') {
			return fmt.Errorf("timestamp contains invalid character %q", c)
		}
	}
	return nil
}

// ValidateVideoID checks that a video ID contains only safe characters.
func ValidateVideoID(id string) error {
	if id == "" {
		return fmt.Errorf("video id is empty")
	}
	if len(id) > 64 {
		return fmt.Errorf("video id too long (%d chars, max 64)", len(id))
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return fmt.Errorf("video id contains invalid character %q", c)
		}
	}
	return nil
}

// AddAllowedHost adds a domain to the allowlist (useful for testing or config overrides).
func AddAllowedHost(host string) {
	allowedHosts[host] = true
}
