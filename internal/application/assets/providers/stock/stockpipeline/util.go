package stockpipeline

import (
	"fmt"
	"strings"
	"time"

	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// F2.10: resolveFolderTarget RETIRED (override brutal). Folder
// resolution now goes through `s.publisher.ResolveFolder(ctx,
// delivery.PublishRequest{Group: seg, DestinationFolderID: currentID})`
// in stockpipeline.run.go, which uses DestinationStock's PathBuilder
// to compute the canonical folder hierarchy. The legacy
// driveutil.EnsureFolderPath walking is gone; the legacy
// `s.driveAdmin driveup.Admin` surface is gone (see
// `internal/infrastructure/drive` import).

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
