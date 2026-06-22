// Package segments provides segment-level helpers for YouTube clip extraction.
package segments

import (
	"fmt"
	"os"
	"strings"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// FileSizeFromPath returns the file size in bytes, or 0 if the file cannot be stat'd.
func FileSizeFromPath(path string) int64 {
	if path == "" {
		return 0
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// BuildClipFilename constructs a canonical YouTube clip filename from video ID,
// timestamps, and a human-readable name.
func BuildClipFilename(videoID string, startSec, endSec int, name string) string {
	slug := textutil.SlugifyWithMax(name, 40)
	if slug == "" {
		slug = "clip"
	}
	if slug[0] >= '0' && slug[0] <= '9' {
		slug = "c_" + slug
	}
	return fmt.Sprintf("yt_%s_%d_%d_%s.mp4", videoID, startSec, endSec, slug)
}

// SanitizeTimestamp validates a timestamp string format (SS, MM:SS, or HH:MM:SS).
func SanitizeTimestamp(ts string) error {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return fmt.Errorf("timestamp is required")
	}
	parts := strings.Split(ts, ":")
	if len(parts) > 3 {
		return fmt.Errorf("invalid timestamp format: %s", ts)
	}
	for _, p := range parts {
		for _, c := range p {
			if c < '0' || c > '9' {
				return fmt.Errorf("invalid timestamp: %s", ts)
			}
		}
	}
	return nil
}
