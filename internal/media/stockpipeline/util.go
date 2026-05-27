package stockpipeline

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// resolveFolderTarget resolves the Google Drive folder ID for upload.
// It walks from the configured stock root folder through subfolder and folderName.
func (s *Service) resolveFolderTarget(ctx context.Context, folderID, subfolder, folderName string) (string, error) {
	currentID := folderID
	if currentID == "" {
		currentID = s.cfg.Drive.RootFolder()
	}
	if currentID == "" {
		return "", fmt.Errorf("no drive folder configured (media_root_folder)")
	}

	if subfolder != "" {
		id, err := s.driveUp.GetOrCreateFolder(ctx, subfolder, currentID)
		if err != nil {
			return "", fmt.Errorf("subfolder %q: %w", subfolder, err)
		}
		currentID = id
	}

	if folderName != "" {
		id, err := s.driveUp.GetOrCreateFolder(ctx, folderName, currentID)
		if err != nil {
			return "", fmt.Errorf("folder %q: %w", folderName, err)
		}
		currentID = id
	}

	return currentID, nil
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
	if strings.Contains(url, "v=") {
		for _, part := range strings.Split(url, "&") {
			if strings.HasPrefix(part, "v=") {
				return strings.TrimPrefix(part, "v=")
			}
		}
	}
	if strings.Contains(url, "youtu.be/") {
		parts := strings.Split(url, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	parts := strings.Split(url, "/")
	return parts[len(parts)-1]
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

// slugify converts a string into a safe filesystem slug.
func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
	s = strings.Trim(s, "_")
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}
