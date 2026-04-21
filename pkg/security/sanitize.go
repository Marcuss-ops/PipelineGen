// Package security provides security utilities for the VeloxEditing API.
package security

import (
	"path/filepath"
	"regexp"
	"strings"
)

// SanitizeFilename removes potentially dangerous characters from a filename
// to prevent path traversal attacks.
func SanitizeFilename(name string) string {
	// Remove path separators and dangerous characters
	name = strings.ReplaceAll(name, "..", "")
	name = strings.ReplaceAll(name, "/", "")
	name = strings.ReplaceAll(name, "\\", "")
	name = strings.ReplaceAll(name, "\x00", "") // null byte

	// Keep only safe characters: alphanumeric, underscore, hyphen, dot, space
	re := regexp.MustCompile(`[^a-zA-Z0-9_.\- ]`)
	name = re.ReplaceAllString(name, "")

	// Trim spaces
	name = strings.TrimSpace(name)

	// Limit length
	if len(name) > 255 {
		name = name[:255]
	}

	// Ensure non-empty
	if name == "" {
		name = "unnamed"
	}

	return name
}

// SanitizePath ensures a path is within a base directory
func SanitizePath(baseDir, requestedPath string) (string, error) {
	// Clean the base directory
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}

	// Join and clean the requested path
	absRequested := filepath.Clean(filepath.Join(absBase, requestedPath))

	// Verify the result is within the base directory
	if !strings.HasPrefix(absRequested, absBase) {
		return "", filepath.ErrBadPattern
	}

	return absRequested, nil
}
