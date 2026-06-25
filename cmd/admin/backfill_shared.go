// Package main — backfill command helpers shared across the
// `backfill-*` admin sub-commands.
//
// The shared appLogger used to live in this file but was a duplicate
// of the canonical one in logger.go (same package, same signature).
// Keeping both caused `cmd/admin` to fail with "appLogger redeclared
// in this block" once logger.go was fixed. The canonical helper now
// lives in logger.go; this file keeps only the file-path sniffing
// helper the backfill commands actually need.
package main

import (
	"path/filepath"
	"strings"
)

// isFilePath checks whether a path string looks like a file path (has a media extension).
func isFilePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".mp4", ".mkv", ".mov", ".avi", ".mp3", ".wav", ".txt", ".json", ".jpg", ".png", ".jpeg":
		return true
	}
	return false
}
