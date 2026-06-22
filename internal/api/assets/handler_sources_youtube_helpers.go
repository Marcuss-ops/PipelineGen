package assets

import (
	"context"
	"fmt"
	"time"
)

// findExistingYouTubeClip checks the clips repo for an existing clip
// matching the given YouTube URL/video ID.
func (h *Handler) findExistingYouTubeClip(ctx context.Context, videoID, sourceURL string, startSec, endSec float64) (string, error) {
	if h.clipsRepo != nil && videoID != "" {
		hasSegment := endSec > startSec
		if id, err := h.clipsRepo.FindByYouTubeVideoID(ctx, videoID, hasSegment, startSec, endSec); err == nil && id != "" {
			return id, nil
		} else if err != nil {
			return "", err
		}
	}
	if h.clipsRepo != nil && sourceURL != "" && !(endSec > startSec) {
		if id, err := h.clipsRepo.FindBySourceURL(ctx, sourceURL); err == nil && id != "" {
			return id, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", nil
}

// formatDuration formats a float64 seconds value as HH:MM:SS.
func formatDuration(sec float64) string {
	d := time.Duration(sec * float64(time.Second))
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// ExtractDriveFolderID moved to internal/api/sources/clips/upload_helpers.go (exported
// as clips.ExtractDriveFolderID). Sources/ callers import it via the clipssources alias.

// cleanFoldName moved to internal/api/sources/clips/upload_helpers.go (exported as
// clips.CleanFolderName). Sources/ callers import it via the clipssources alias.

// buildDriveDescription moved to internal/api/sources/clips/upload_helpers.go (exported
// as clips.BuildDriveDescription). Sources/ callers import it via the clipssources alias.

// updateCumulativeMetadataJSON refactored to a free function: clips.UpdateCumulativeMetadataJSON
// in upload_helpers.go takes driveUploader + tempPath as explicit params. Sources/
// callers (register_from_youtube.go) call via clipssources.UpdateCumulativeMetadataJSON(...)
// instead of h.updateCumulativeMetadataJSON.

// cleanupLegacyMetadataJSON moved inline into clips.UpdateCumulativeMetadataJSON
// (now an internal package-private helper there). Sources/ no longer needs its own
// copy.
