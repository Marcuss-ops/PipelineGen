// Package security provides security utilities for the VeloxEditing API.
package security

import (
	"path/filepath"
	"strings"
)

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
