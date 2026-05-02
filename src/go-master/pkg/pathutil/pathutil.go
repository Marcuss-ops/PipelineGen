package pathutil

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Slug creates a URL-safe slug from a string.
// Converts to lowercase, keeps only alphanumerics, replaces spaces/special chars with underscores.
func Slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}

	var b strings.Builder
	lastUnderscore := false

	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case r == ' ' || r == '-' || r == '_' || r == '.':
			if b.Len() > 0 && !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}

	return strings.Trim(b.String(), "_")
}

// SafeFilename sanitizes a filename, keeping only safe characters.
// Ensures the file stays within the base directory (prevents path traversal).
func SafeFilename(outputDir, filename string) (string, error) {
	// Only keep the base filename to prevent path traversal
	cleanName := filepath.Base(filename)

	// Additional slugify: remove any path separators that might have passed through
	cleanName = strings.ReplaceAll(cleanName, "/", "")
	cleanName = strings.ReplaceAll(cleanName, "\\", "")

	outputPath := filepath.Join(outputDir, cleanName)

	// Verify the result is within outputDir
	absOutputDir, err := filepath.Abs(outputDir)
	if err != nil {
		return "", err
	}
	absOutputPath, err := filepath.Abs(outputPath)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absOutputDir, absOutputPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid filename: path traversal detected")
	}

	return outputPath, nil
}

// SafeFolderName creates a safe folder name from a string.
// Similar to Slug but allows dots and ensures it's not empty.
func SafeFolderName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "untitled"
	}

	var b strings.Builder
	lastUnderscore := false

	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case r == ' ' || r == '-' || r == '_' || r == '.':
			if b.Len() > 0 && !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		default:
			if r < 128 {
				if b.Len() > 0 && !lastUnderscore {
					b.WriteByte('_')
					lastUnderscore = true
				}
			}
		}
	}

	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "untitled"
	}
	return out
}

// SafeJoin safely joins a base directory with a filename, preventing path traversal.
func SafeJoin(baseDir, filename string) (string, error) {
	if filepath.IsAbs(filename) {
		return "", fmt.Errorf("filename must not be absolute")
	}

	// Clean the filename to remove any ".." or "." components
	cleanName := filepath.Clean(filename)
	if strings.Contains(cleanName, "..") {
		return "", fmt.Errorf("path traversal detected in filename")
	}

	outputPath := filepath.Join(baseDir, cleanName)

	// Verify the result is within baseDir
	absBaseDir, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	absOutputPath, err := filepath.Abs(outputPath)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absBaseDir, absOutputPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal detected")
	}

	return outputPath, nil
}
