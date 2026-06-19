package sources

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	executil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
)

// resolveDownloadedPath finds the actual file yt-dlp wrote, handling
// extension templates like .%(ext)s → .mp4.
func resolveDownloadedPath(template string) string {
	// 1. If template has %(ext)s, strip it and try common extensions
	base := strings.TrimSuffix(template, ".%(ext)s")
	if base != template {
		for _, ext := range []string{".mp4", ".mkv", ".webm"} {
			if _, err := os.Stat(base + ext); err == nil {
				return base + ext
			}
		}
	}
	// 2. Try common extensions appended to template
	for _, ext := range []string{".mp4", ".mkv", ".webm"} {
		if _, err := os.Stat(template + ext); err == nil {
			return template + ext
		}
	}
	// 3. Try the template directly
	if _, err := os.Stat(template); err == nil {
		return template
	}
	// 4. Last resort: glob for any file starting with the template name
	matches, _ := filepath.Glob(template + ".*")
	for _, m := range matches {
		if !strings.HasSuffix(m, ".part") && !strings.HasSuffix(m, ".ytdl") {
			return m
		}
	}
	return ""
}

// cutVideoSegment extracts a segment from a video file using ffmpeg.
func cutVideoSegment(inputPath, outputPath string, startSec, endSec float64) error {
	duration := endSec - startSec
	args := []string{
		"-y",
		"-ss", fmt.Sprintf("%.3f", startSec),
		"-i", inputPath,
		"-t", fmt.Sprintf("%.3f", duration),
		"-c", "copy",
		"-avoid_negative_ts", "make_zero",
		outputPath,
	}
	result, err := executil.Run(context.Background(), "ffmpeg", args, executil.DefaultOptions())
	if err != nil {
		return fmt.Errorf("ffmpeg cut failed: %w, output: %s", err, strings.TrimSpace(result.Output))
	}
	return nil
}

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
