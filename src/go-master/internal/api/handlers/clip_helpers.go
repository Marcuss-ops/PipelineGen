package handlers

import (
	"fmt"
	"strings"

	"velox/go-master/internal/clip"
)

// isVideoFile checks if a file is a video based on MIME type or extension
// Delegates to the exported clip.IsVideoFile for consistency
func isVideoFile(mimeType, filename string) bool {
	return clip.IsVideoFile(mimeType, filename)
}

// cleanClipName cleans a filename for display
// Delegates to the exported clip.CleanClipName for consistency
func cleanClipName(filename string) string {
	return clip.CleanClipName(filename)
}

// sanitizeFolderName removes invalid characters from folder names
func sanitizeFolderName(name string) string {
	// Remove invalid characters
	invalid := []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*"}
	for _, char := range invalid {
		name = strings.ReplaceAll(name, char, "")
	}
	// Trim spaces
	name = strings.TrimSpace(name)
	// Limit length
	if len(name) > 100 {
		name = name[:100]
	}
	return name
}

// getThumbnailURL returns a thumbnail URL for a Drive file
func getThumbnailURL(fileID string) string {
	// Google Drive doesn't provide direct thumbnails for all videos
	// Return a placeholder or generate a Drive thumbnail URL
	return fmt.Sprintf("https://drive.google.com/thumbnail?id=%s", fileID)
}
