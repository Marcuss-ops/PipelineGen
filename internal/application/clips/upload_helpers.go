// Package clips (upload_helpers) — pure-string helpers for the upload
// flow. PR 7 (codex/asset-manifest-cutover, June 2026) collapsed the
// pre-cutover metadata writers into one canonical Service: every
// per-asset metadata write now routes through
// internal/application/assets/manifest.Service (see PR 7 spec +
// Definition-of-Done gate).
//
// The functions that remain are pure string utilities (no I/O,
// no lock, no Drive touchpoints) so this file stays infra-free
// per Wave 14 PR2 + AGENTS.md Pattern 8.
package clips

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var driveFolderIDRegex = regexp.MustCompile(`/folders/([a-zA-Z0-9_-]+)`)

// ExtractDriveFolderID extracts the folder ID from a Google Drive URL or
// returns the input unchanged if it's already a raw ID. Used by both
// clip_upload flows (clips upload_helpers) and the legacy
// register_from_youtube flow (sources package). Exported so callers
// cross-package can use it without an alias.
func ExtractDriveFolderID(input string) string {
	if input == "" {
		return ""
	}
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		if parsed, err := url.Parse(input); err == nil {
			if matches := driveFolderIDRegex.FindStringSubmatch(parsed.Path); len(matches) > 1 {
				return matches[1]
			}
		}
	}
	return input
}

// CleanFolderName normalizes a folder name for comparison.
func CleanFolderName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}

// BuildDriveDescription builds a description string for the Drive file.
// Pure function: no-op on migration, signature unchanged from the
// api-side copy.
func BuildDriveDescription(name, reqDescription, metaDescription string, tags []string, category, source, urlVal, videoID string) string {
	var parts []string

	parts = append(parts, fmt.Sprintf("Name: %s", name))

	if category != "" {
		parts = append(parts, fmt.Sprintf("Category: %s", category))
	}
	if source != "" {
		parts = append(parts, fmt.Sprintf("Source: %s", source))
	}
	if videoID != "" {
		parts = append(parts, fmt.Sprintf("YouTube ID: %s", videoID))
	}
	if urlVal != "" {
		parts = append(parts, fmt.Sprintf("URL: %s", urlVal))
	}

	desc := reqDescription
	if desc == "" {
		desc = metaDescription
	}
	if desc != "" {
		if len(desc) > 500 {
			desc = desc[:500] + "..."
		}
		parts = append(parts, fmt.Sprintf("Description: %s", desc))
	}

	if len(tags) > 0 {
		parts = append(parts, fmt.Sprintf("Tags: %s", strings.Join(tags, ", ")))
	}

	return strings.Join(parts, "\n")
}
