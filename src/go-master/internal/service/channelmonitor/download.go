package channelmonitor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

func shouldUploadPerClipText() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("VELOX_ENABLE_PER_CLIP_TXT")))
	return v == "true" || v == "1" || v == "yes"
}

// downloadAndUploadClips downloads highlight clips from a YouTube video
// and uploads them to the specified Drive folder.
func (m *Monitor) downloadAndUploadClips(ctx context.Context, video youtube.SearchResult, highlights []HighlightSegment, folderID, _ string, _ bool, maxDuration int) ([]ClipResult, error) {
	if m.driveClient == nil {
		return nil, fmt.Errorf("drive client not configured")
	}

	var results []ClipResult
	tmpDir, err := os.MkdirTemp("", "channel-monitor-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Limit to 5 clips max
	maxClips := 5
	if len(highlights) < maxClips {
		maxClips = len(highlights)
	}

	for i := 0; i < maxClips; i++ {
		seg := highlights[i]
		clipName := fmt.Sprintf("clip_%s_%d", video.ID, i)
		clipFile := filepath.Join(tmpDir, clipName+".mp4")

		// Download clip using yt-dlp with time range
		if err := m.downloadClipFn(ctx, video.ID, seg.StartSec, seg.Duration, clipFile); err != nil {
			logger.Warn("Failed to download clip",
				zap.String("video_id", video.ID),
				zap.Int("segment", i),
				zap.Error(err),
			)
			continue
		}

		// Check file exists and has reasonable size
		info, err := os.Stat(clipFile)
		if err != nil || info.Size() < 1000 {
			logger.Warn("Clip file too small or missing",
				zap.String("file", clipFile),
			)
			continue
		}

		// Upload to Drive
		filename := sanitizeFolderName(video.Title) + fmt.Sprintf("_clip%d.mp4", i+1)
		driveFileID, err := m.driveClient.UploadFile(ctx, clipFile, folderID, filename)
		if err != nil {
			logger.Warn("Failed to upload clip to Drive",
				zap.String("video_id", video.ID),
				zap.Int("segment", i),
				zap.Error(err),
			)
			continue
		}

		if shouldUploadPerClipText() {
			// Optional legacy behavior: upload per-clip txt only when explicitly enabled.
			txtContent := fmt.Sprintf("Source: %s\nTitle: %s\nSegment: %d-%d sec\nHighlight: %s",
				video.ID, video.Title, seg.StartSec, seg.EndSec, seg.Text)
			txtFile := filepath.Join(tmpDir, clipName+".txt")
			if err := os.WriteFile(txtFile, []byte(txtContent), 0644); err == nil {
				txtFilename := sanitizeFolderName(video.Title) + fmt.Sprintf("_clip%d.txt", i+1)
				if txtFileID, err := m.driveClient.UploadFile(ctx, txtFile, folderID, txtFilename); err == nil {
					results = append(results, ClipResult{
						VideoID:      video.ID,
						VideoTitle:   video.Title,
						ClipFile:     filename,
						Duration:     seg.Duration,
						Description:  seg.Text,
						DriveFileID:  driveFileID,
						DriveFileURL: fmt.Sprintf("https://drive.google.com/file/d/%s/view", driveFileID),
						TxtFileID:    txtFileID,
					})
					continue
				}
			}
		}

		// Without txt file
		results = append(results, ClipResult{
			VideoID:      video.ID,
			VideoTitle:   video.Title,
			ClipFile:     filename,
			Duration:     seg.Duration,
			Description:  seg.Text,
			DriveFileID:  driveFileID,
			DriveFileURL: fmt.Sprintf("https://drive.google.com/file/d/%s/view", driveFileID),
		})
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no clips could be downloaded/uploaded for video %s", video.ID)
	}

	return results, nil
}

// downloadClip downloads a segment of a YouTube video using yt-dlp
func (m *Monitor) downloadClip(ctx context.Context, videoID string, startSec, duration int, outputFile string) error {
	url := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	maxDuration := m.config.MaxClipDuration
	if maxDuration <= 0 {
		maxDuration = 60
	}
	if duration <= 0 || duration > maxDuration {
		duration = maxDuration
	}

	dlCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	return youtube.DownloadSection(dlCtx, youtube.SectionDownloadOptions{
		YtDlpPath:          m.config.YtDlpPath,
		URL:                url,
		OutputFile:         outputFile,
		StartSec:           startSec,
		Duration:           duration,
		MaxHeight:          1080,
		CookiesFile:        m.config.CookiesPath,
		DefaultCookiesFile: "",
		MaxFilesize:        "1G",
	})
}
