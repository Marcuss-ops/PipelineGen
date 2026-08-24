// Package adapters — postprocessor_image.go: image-related types.
// Owns: SceneImage.
package adapters

import (
	"fmt"
	"strings"
)

// SceneImage is a single scene-image outcome from ImageProcessor.
// PR 9: images map to model-defined scenes 1:1.
type SceneImage struct {
	Index       int
	Text        string // scene text used as the generation prompt
	URL         string // public URL of the generated image
	DriveFileID string
}

func SceneImageDriveLink(img SceneImage) string {
	url := strings.TrimSpace(img.URL)
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return url
	}
	if img.DriveFileID != "" {
		return fmt.Sprintf("https://drive.google.com/file/d/%s/view", img.DriveFileID)
	}
	return url
}
