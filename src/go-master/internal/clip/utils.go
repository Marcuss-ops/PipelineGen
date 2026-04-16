package clip

import (
	"path/filepath"
	"strings"
)

func containsTag(tags []string, tag string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

func IsVideoFile(mimeType, filename string) bool {
	videoMimeTypes := []string{
		"video/mp4",
		"video/quicktime",
		"video/x-msvideo",
		"video/x-matroska",
		"video/webm",
		"video/mpeg",
	}

	for _, vt := range videoMimeTypes {
		if strings.Contains(mimeType, vt) {
			return true
		}
	}

	videoExtensions := []string{".mp4", ".mov", ".avi", ".mkv", ".webm", ".m4v", ".flv", ".wmv"}
	lowerFilename := strings.ToLower(filename)
	for _, ext := range videoExtensions {
		if strings.HasSuffix(lowerFilename, ext) {
			return true
		}
	}

	return false
}

func CleanClipName(filename string) string {
	name := strings.TrimSuffix(filename, filepath.Ext(filename))
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.ReplaceAll(name, "-", " ")
	name = strings.Join(strings.Fields(name), " ")
	return strings.TrimSpace(name)
}
