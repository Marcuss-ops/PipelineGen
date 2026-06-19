// Package pathutil provides filesystem path and folder naming helpers.
package pathutil

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// SafeFolderName sanitises a name for use as a filesystem folder.
// Replaces unsafe characters with underscores and trims whitespace.
func SafeFolderName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "untitled"
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == ' ' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	result := strings.TrimSpace(b.String())
	if result == "" {
		return "untitled"
	}
	return result
}

// BuildTimestampedSlug creates a slug from name with a timestamp prefix.
func BuildTimestampedSlug(name, ext string) string {
	base := SafeFolderName(name)
	ts := time.Now().UTC().Format("20060102-150405")
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return fmt.Sprintf("%s-%s%s", ts, base, ext)
}

// ExtractStyleFromPath extracts the style name from a relative path.
// Example: "Cinematic/Italy/scene_01.jpg" → "Cinematic"
func ExtractStyleFromPath(relPath string) string {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	parts := strings.SplitN(relPath, "/", 2)
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}
	return ""
}
