package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"

	executil "github.com/Marcuss-ops/PipelineGen/internal/platform"
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
	result, err := executil.Run(context.Background(), "ffmpeg", args, executil.DefaultExecOptions())
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


var driveFolderIDRegex = regexp.MustCompile(`/folders/([a-zA-Z0-9_-]+)`)

// ExtractDriveFolderID extracts the folder ID from a Google Drive URL or returns the input if it's already a raw ID.
func ExtractDriveFolderID(input string) string {
	if input == "" {
		return ""
	}
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		if parsed, err := url.Parse(input); err == nil {
			if matches := driveFolderIDRegex.FindStringSubmatch(parsed.Path); len(matches) > 1 {
				return matches[1]
			}
		}
	}
	return input
}

// cleanFoldName normalizes a folder name for comparison.
func cleanFoldName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}

// buildDriveDescription builds a description string for the Drive file.
func buildDriveDescription(name, reqDescription, metaDescription string, tags []string, category, source, url, videoID string) string {
	var parts []string

	parts = append(parts, fmt.Sprintf("Name: %s", name))

	if category != "" {
		parts = append(parts, fmt.Sprintf("Category: %s", category))
	}
	if source != "" {
		parts = append(parts, fmt.Sprintf("Source: %s", source))
	}
	if videoID != "" {
		parts = append(parts, fmt.Sprintf("YouTube ID: %s", videoID))
	}
	if url != "" {
		parts = append(parts, fmt.Sprintf("URL: %s", url))
	}

	desc := reqDescription
	if desc == "" {
		desc = metaDescription
	}
	if desc != "" {
		if len(desc) > 500 {
			desc = desc[:500] + "..."
		}
		parts = append(parts, fmt.Sprintf("Description: %s", desc))
	}

	if len(tags) > 0 {
		parts = append(parts, fmt.Sprintf("Tags: %s", strings.Join(tags, ", ")))
	}

	return strings.Join(parts, "\n")
}

// updateCumulativeMetadataJSON maintains a single metadata.json per group folder.
func (h *Handler) updateCumulativeMetadataJSON(ctx context.Context, folderID, clipID string, newEntry map[string]interface{}, log *zap.Logger) {
	const metaFilename = "metadata.json"

	var existing []map[string]interface{}
	query := fmt.Sprintf("'%s' in parents and trashed = false and name = '%s'", folderID, metaFilename)
	list, err := h.driveUploader.Service.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		log.Warn("failed to list metadata.json", zap.Error(err))
	} else if len(list.Files) > 0 {
		existingFileID := list.Files[0].Id
		body, _, dlErr := h.driveUploader.DownloadFile(ctx, existingFileID)
		if dlErr == nil && body != nil {
			defer body.Close()
			var raw []map[string]interface{}
			if decErr := json.NewDecoder(body).Decode(&raw); decErr == nil {
				existing = raw
			}
		}
		if err := h.driveUploader.TrashFile(ctx, existingFileID); err != nil {
			log.Warn("failed to trash old metadata.json", zap.Error(err))
		}
	}

	found := false
	for i, entry := range existing {
		if id, ok := entry["clip_id"].(string); ok && id == clipID {
			existing[i] = newEntry
			found = true
			break
		}
	}
	if !found {
		existing = append(existing, newEntry)
	}

	jsonBytes, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		log.Warn("failed to marshal cumulative metadata json", zap.Error(err))
		return
	}
	metaTempPath := filepath.Join(h.cfg.Storage.TempPath(), fmt.Sprintf("meta_%s_%d.json", clipID, time.Now().UnixNano()))
	if err := os.WriteFile(metaTempPath, jsonBytes, 0644); err != nil {
		log.Warn("failed to write metadata json temp file", zap.Error(err))
		return
	}
	if _, err := h.driveUploader.UploadFile(ctx, metaTempPath, folderID, metaFilename); err != nil {
		log.Warn("failed to upload metadata.json to Drive", zap.Error(err))
	} else {
		log.Info("uploaded cumulative metadata.json to Drive", zap.Int("entries", len(existing)))
	}
	os.Remove(metaTempPath)

	h.cleanupLegacyMetadataJSON(ctx, folderID, log)
}

// cleanupLegacyMetadataJSON removes old per-video metadata files.
func (h *Handler) cleanupLegacyMetadataJSON(ctx context.Context, folderID string, log *zap.Logger) {
	if h.driveUploader == nil || h.driveUploader.Service == nil || folderID == "" {
		return
	}
	query := fmt.Sprintf("'%s' in parents and trashed = false and name contains '.json' and name != 'metadata.json'", folderID)
	list, err := h.driveUploader.Service.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		return
	}
	for _, f := range list.Files {
		log.Info("cleaning up legacy metadata json", zap.String("file_id", f.Id), zap.String("name", f.Name))
		if err := h.driveUploader.TrashFile(ctx, f.Id); err != nil {
			log.Warn("failed to trash legacy metadata json", zap.String("file_id", f.Id), zap.Error(err))
		}
	}
}
