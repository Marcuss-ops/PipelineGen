package mediaasset

import (
	"os"
	"path/filepath"
	"strings"
)

func SafeName(name string) string {
	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		":", "-",
		"*", "-",
		"?", "-",
		"\"", "-",
		"<", "-",
		">", "-",
		"|", "-",
	)
	return replacer.Replace(strings.TrimSpace(name))
}

func TmpPath(outputDir, filename string) string {
	return filepath.Join(outputDir, "tmp_"+filename)
}

func OutputPath(outputDir, filename string) string {
	return filepath.Join(outputDir, filename)
}

// ResolveDownloadedFile attempts to find the actual downloaded file.
// It first checks if expectedPath exists, then falls back to glob matching
// to handle cases where yt-dlp saves with different extensions (e.g. m3u8 streams).
func ResolveDownloadedFile(expectedPath string) string {
	if _, err := os.Stat(expectedPath); err == nil {
		return expectedPath
	}

	pattern := strings.TrimSuffix(expectedPath, filepath.Ext(expectedPath)) + "*"
	matches, err := filepath.Glob(pattern)
	if err == nil && len(matches) > 0 {
		for _, m := range matches {
			if info, err := os.Stat(m); err == nil && !info.IsDir() {
				return m
			}
		}
	}

	return expectedPath
}
