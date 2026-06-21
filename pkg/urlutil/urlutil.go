// Package urlutil provides URL parsing helpers for YouTube and Google Drive
// links used across the codebase.
package urlutil

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// driveFolderIDRegex matches the folder id in Drive folder URLs:
// https://drive.google.com/drive/folders/<id>.
// Mirrors the regex that had been living in
// internal/api/sources/clips/upload_helpers.go (now an alias to this
// function) so behaviour is unchanged post-migration.
var driveFolderIDRegex = regexp.MustCompile(`/folders/([a-zA-Z0-9_-]+)`)

// ExtractVideoID extracts the video ID from a YouTube URL.
// Supports youtu.be, youtube.com/watch, /shorts/, /embed/, /live/,
// and mobile (m.youtube.com) variants.
func ExtractVideoID(rawURL string) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("empty URL")
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	// Handle youtu.be short links
	if parsed.Hostname() == "youtu.be" {
		path := strings.TrimPrefix(parsed.Path, "/")
		if path != "" {
			if idx := strings.Index(path, "?"); idx != -1 {
				path = path[:idx]
			}
			return path, nil
		}
	}

	// Handle youtube.com URLs (including m.youtube.com, www.youtube.com, etc.)
	if strings.Contains(parsed.Hostname(), "youtube.com") {
		if parsed.Path == "/watch" {
			v := parsed.Query().Get("v")
			if v == "" {
				return "", fmt.Errorf("no video ID in watch URL")
			}
			return v, nil
		}
		if strings.HasPrefix(parsed.Path, "/shorts/") {
			id := strings.TrimPrefix(parsed.Path, "/shorts/")
			if idx := strings.Index(id, "?"); idx != -1 {
				id = id[:idx]
			}
			return id, nil
		}
		if strings.HasPrefix(parsed.Path, "/embed/") {
			id := strings.TrimPrefix(parsed.Path, "/embed/")
			if idx := strings.Index(id, "?"); idx != -1 {
				id = id[:idx]
			}
			return id, nil
		}
		if strings.HasPrefix(parsed.Path, "/live/") {
			id := strings.TrimPrefix(parsed.Path, "/live/")
			if idx := strings.Index(id, "?"); idx != -1 {
				id = id[:idx]
			}
			return id, nil
		}
		return "", fmt.Errorf("unrecognized youtube.com URL path: %s", parsed.Path)
	}

	return "", fmt.Errorf("not a YouTube URL")
}

// FileIDFromDriveLink extracts the file ID from a Google Drive file URL.
// Supports the most common URL shapes used by the project:
//
//   - https://drive.google.com/file/d/<id>/view
//   - https://drive.google.com/file/d/<id>/edit
//   - https://drive.google.com/uc?id=<id>
//   - https://drive.google.com/open?id=<id>
//   - bare <id>
//
// Returns ("", nil) when the input is empty. Returns an error when the URL
// looks like a Drive URL but no ID can be extracted.
func FileIDFromDriveLink(rawLink string) (string, error) {
	link := strings.TrimSpace(rawLink)
	if link == "" {
		return "", nil
	}

	// Bare ID? Allow alphanumeric, dash, underscore.
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

	host := strings.ToLower(parsed.Hostname())
	if host != "drive.google.com" {
		return "", fmt.Errorf("not a Drive URL: host=%q", host)
	}

	// /file/d/<id>
	if strings.HasPrefix(parsed.Path, "/file/d/") {
		rest := strings.TrimPrefix(parsed.Path, "/file/d/")
		if idx := strings.Index(rest, "/"); idx >= 0 {
			rest = rest[:idx]
		}
		id := strings.TrimSpace(rest)
		if id == "" {
			return "", fmt.Errorf("empty file ID in Drive URL: %q", rawLink)
		}
		return id, nil
	}

	// /uc or /open — ID in query string
	if parsed.Path == "/uc" || parsed.Path == "/open" {
		id := parsed.Query().Get("id")
		if id == "" {
			return "", fmt.Errorf("no id query param in Drive URL: %q", rawLink)
		}
		return id, nil
	}

	if strings.HasPrefix(parsed.Path, "/drive/folders/") {
		return "", fmt.Errorf("Drive URL points to a folder, not a file: %q", rawLink)
	}

	return "", fmt.Errorf("unrecognised Drive URL path: %q", parsed.Path)
}

// FolderIDFromDriveLink extracts the folder ID from a Google Drive folder
// URL. Behaviour is permissive: if the input is empty, the function
// returns "". If the input is not a URL (or is a URL whose path doesn't
// match the folder regex), the function returns the input unchanged,
// preserving the legacy contract of
// internal/api/sources/clips.ExtractDriveFolderID — callers in admin
// handlers (CreateFolders, ResolveByIDs) treat the return value as an
// opaque folder-id-or-input.
//
// Supported URL shapes:
//
//   - https://drive.google.com/drive/folders/<id>
//   - bare <id>  (returned as-is)
//
// Returns "" only when input is empty after TrimSpace.
//
// This function is the canonical replacement for
// internal/api/sources/clips.ExtractDriveFolderID, which is now an
// alias (clips/upload_helpers.go::ExtractDriveFolderID → this fn).
func FolderIDFromDriveLink(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		return trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return trimmed
	}
	matches := driveFolderIDRegex.FindStringSubmatch(parsed.Path)
	if len(matches) < 2 {
		return trimmed
	}
	return matches[1]
}
