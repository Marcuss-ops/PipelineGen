package lessons

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// chapterAnchor creates an HTML anchor from chapter index and title.
// Reusable by any component that needs to generate internal links.
func chapterAnchor(index int, title string) string {
	return textutil.Slugify(fmt.Sprintf("capitolo-%d-%s", index, title))
}

// escapeYAMLString escapes a string for YAML front matter.
// Reusable by any component that writes YAML metadata.
func escapeYAMLString(s string) string {
	if strings.Contains(s, ":") || strings.Contains(s, "#") || strings.Contains(s, "\"") {
		s = strings.ReplaceAll(s, "\"", "\\\"")
		s = "\"" + s + "\""
	}
	return s
}

// resolveLocalImagePath converts a relative image path to an absolute filesystem path.
// Tries common locations (data/ + pathRel, pathRel directly).
// Reusable by any component that needs to locate downloaded assets.
func resolveLocalImagePath(pathRel string) string {
	if pathRel == "" {
		return ""
	}
	if filepath.IsAbs(pathRel) {
		return pathRel
	}
	candidates := []string{
		filepath.Join("data", pathRel),
		pathRel,
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

// fileExists checks if a file exists and is not a directory.
// Reusable utility.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
