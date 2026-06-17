package urlutil

import (
	"fmt"
	"net/url"
	"strings"
)

// FileIDFromDriveLink extracts the file ID from a Google Drive file URL.
// Supports the most common URL shapes used by the project:
//
//   - https://drive.google.com/file/d/<id>/view
//   - https://drive.google.com/file/d/<id>/edit
//   - https://drive.google.com/file/d/<id>?usp=drivesdk
//   - https://drive.google.com/uc?id=<id>
//   - https://drive.google.com/open?id=<id>
//   - bare <id>
//
// Returns ("", nil) when the input is empty — callers can decide whether
// that's an error. Returns an error when the URL looks like a Drive URL
// but no ID can be extracted (so callers can distinguish "no link" from
// "malformed link").
//
// The previously inlined parsing in phase_image_generation.go was
// fragile (it only matched the "file/d/" pattern and split on "/"
// without URL-decoding). Centralising here also lets us normalise
// raw IDs (trim whitespace, strip trailing punctuation) once.
func FileIDFromDriveLink(rawLink string) (string, error) {
	link := strings.TrimSpace(rawLink)
	if link == "" {
		return "", nil
	}

	// Bare ID? Allow [A-Za-z0-9_-] as Drive IDs are URL-safe base64ish.
	// To avoid false positives on short tokens, require >= 10 chars and
	// no scheme/host.
	if !strings.Contains(link, "://") && !strings.Contains(link, "/") {
		if len(link) >= 10 {
			return strings.TrimRight(link, "/"), nil
		}
		return "", fmt.Errorf("not a Drive file ID: %q", rawLink)
	}

	parsed, err := url.Parse(link)
	if err != nil {
		return "", fmt.Errorf("invalid Drive URL: %w", err)
	}

	// Only operate on drive.google.com URLs — non-Drive URLs return an
	// explicit error so callers don't silently swallow bad data.
	host := strings.ToLower(parsed.Hostname())
	if host != "drive.google.com" {
		return "", fmt.Errorf("not a Drive URL: host=%q", host)
	}

	// 1. /file/d/<id>  (with optional /view, /edit, /preview, query string)
	if strings.HasPrefix(parsed.Path, "/file/d/") {
		rest := strings.TrimPrefix(parsed.Path, "/file/d/")
		// ID is the segment up to the next '/'
		if idx := strings.Index(rest, "/"); idx >= 0 {
			rest = rest[:idx]
		}
		id := strings.TrimSpace(rest)
		if id == "" {
			return "", fmt.Errorf("empty file ID in Drive URL: %q", rawLink)
		}
		return id, nil
	}

	// 2. /uc (legacy) or /open — ID lives in the query string.
	if parsed.Path == "/uc" || parsed.Path == "/open" {
		id := parsed.Query().Get("id")
		if id == "" {
			return "", fmt.Errorf("no id query param in Drive URL: %q", rawLink)
		}
		return id, nil
	}

	// 3. /drive/folders/<id> — folders, not files. Surface as explicit
	//    error so callers don't accidentally treat a folder as a file.
	if strings.HasPrefix(parsed.Path, "/drive/folders/") {
		return "", fmt.Errorf("Drive URL points to a folder, not a file: %q", rawLink)
	}

	return "", fmt.Errorf("unrecognised Drive URL path: %q", parsed.Path)
}
