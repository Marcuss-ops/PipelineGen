package stockpipeline

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	driveutil "velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/urlutil"
)

// resolveFolderTarget resolves the Google Drive folder ID for upload.
// It walks from the configured stock root folder through subfolder and folderName.
func (s *Service) resolveFolderTarget(ctx context.Context, folderID, subfolder, folderName string) (string, error) {
	currentID := folderID
	if currentID == "" {
		currentID = s.cfg.Drive.StockFolder()
	}
	if currentID == "" {
		return "", fmt.Errorf("no drive folder configured (media_root_folder)")
	}

	// Build segment list: only prepend "Stock" when resolving from the media root.
	var segs []string
	isMediaRoot := s.cfg != nil && currentID == s.cfg.Drive.MediaRootFolder
	if isMediaRoot {
		segs = append(segs, "Stock")
	}
	if subfolder != "" {
		segs = append(segs, subfolder)
	}
	if folderName != "" {
		segs = append(segs, folderName)
	}

	if len(segs) == 0 {
		return currentID, nil
	}

	return driveutil.EnsureFolderPath(ctx, s.driveUp, currentID, segs...)
}

// formatDuration converts a float64 seconds value to HH:MM:SS.mmm format.
func formatDuration(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	d := time.Duration(sec * float64(time.Second))
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	ms := (d - s*time.Second) / time.Millisecond
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}

// extractVideoID extracts the video ID from a YouTube URL.
func extractVideoID(url string) string {
	if id, err := urlutil.ExtractVideoID(url); err == nil {
		return id
	}
	if strings.Contains(url, "v=") {
		for _, part := range strings.Split(url, "&") {
			if strings.HasPrefix(part, "v=") {
				return strings.TrimPrefix(part, "v=")
			}
		}
	}
	parts := strings.Split(strings.TrimSpace(url), "/")
	if len(parts) == 0 {
		return url
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

// resolveActualPath checks for the actual file path by trying common extensions.
func resolveActualPath(basePath string) string {
	if _, err := os.Stat(basePath); err == nil {
		return basePath
	}
	if _, err := os.Stat(basePath + ".mp4"); err == nil {
		return basePath + ".mp4"
	}
	if _, err := os.Stat(basePath + ".mkv"); err == nil {
		return basePath + ".mkv"
	}
	if _, err := os.Stat(basePath + ".webm"); err == nil {
		return basePath + ".webm"
	}
	return ""
}

func uniqueRepeat(value string, count int) []string {
	if count <= 0 || value == "" {
		return nil
	}
	out := make([]string, count)
	for i := range out {
		out[i] = value
	}
	return out
}
