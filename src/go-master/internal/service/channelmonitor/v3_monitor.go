package channelmonitor

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"
)

// V3Monitor is the experimental Gemini/Ollama pipeline kept separate from the
// active channel monitor to avoid mixing two different flows.
type V3Monitor struct {
	db        *sql.DB
	ytData    *youtube.DataAPIBackend
	ytdlp     youtube.Client
	ytdlpPath string
	ollamaURL string
}

// NewV3Monitor builds the experimental V3 monitor.
func NewV3Monitor(db *sql.DB, ytData *youtube.DataAPIBackend, ytdlp youtube.Client, ollamaURL string) *V3Monitor {
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}
	return &V3Monitor{
		db:        db,
		ytData:    ytData,
		ytdlp:     ytdlp,
		ytdlpPath: "yt-dlp",
		ollamaURL: ollamaURL,
	}
}

// RunOnce processes all monitored channels once.
func (m *V3Monitor) RunOnce(ctx context.Context) error {
	if err := m.checkOllamaHealth(ctx); err != nil {
		return fmt.Errorf("ollama health check failed, skipping pipeline: %w", err)
	}

	channels, err := m.listMonitoredChannels(ctx)
	if err != nil {
		return err
	}

	for _, ch := range channels {
		logger.Info("Checking channel for new uploads", zap.String("channel_id", ch.ChannelID))

		items, err := m.ytData.GetPlaylistItems(ctx, ch.UploadsPlaylistID, 10)
		if err != nil {
			logger.Error("Failed to get playlist items", zap.Error(err))
			continue
		}

		for _, item := range items {
			if m.videoExists(ctx, item.ID) {
				continue
			}

			info, err := m.ytData.GetVideo(ctx, item.ID)
			if err != nil {
				logger.Error("Failed to get video metadata", zap.Error(err))
				continue
			}

			classification, err := m.classifyWithGemma(ctx, info)
			if err != nil {
				logger.Warn("Gemma classification failed", zap.Error(err))
			}

			if err := m.saveVideoMetadata(ctx, info, classification); err != nil {
				logger.Error("Failed to save video metadata", zap.Error(err))
				continue
			}

			logger.Info("New video discovered and indexed",
				zap.String("video_id", info.ID),
				zap.String("title", info.Title),
				zap.String("category", classification.Category),
			)

			m.queueDownloadJob(ctx, info, classification)
		}

		_ = m.updateLastChecked(ctx, ch.ChannelID)
	}

	return nil
}

// MonitoredChannel is a local view of the monitor registry.
type MonitoredChannel struct {
	ChannelID         string
	UploadsPlaylistID string
}

func (m *V3Monitor) listMonitoredChannels(ctx context.Context) ([]MonitoredChannel, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT channel_id, uploads_playlist_id FROM monitored_channels")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []MonitoredChannel
	for rows.Next() {
		var ch MonitoredChannel
		if err := rows.Scan(&ch.ChannelID, &ch.UploadsPlaylistID); err != nil {
			return nil, err
		}
		result = append(result, ch)
	}
	return result, nil
}

func (m *V3Monitor) processVideoV3(ctx context.Context, videoID string, _ MonitoredChannel) error {
	maxRetries := 3
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := m.processVideoV3Once(ctx, videoID)
		if err == nil {
			return nil
		}

		lastErr = err
		logger.Warn("Video processing failed, retrying",
			zap.String("video_id", videoID),
			zap.Int("attempt", attempt),
			zap.Int("max_retries", maxRetries),
			zap.Error(err))

		if attempt < maxRetries {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	return fmt.Errorf("failed after %d attempts: %w", maxRetries, lastErr)
}

func (m *V3Monitor) processVideoV3Once(ctx context.Context, videoID string) error {
	info, err := m.ytData.GetVideo(ctx, videoID)
	if err != nil {
		return fmt.Errorf("metadata fetch failed: %w", err)
	}

	transcript, err := m.ytdlp.GetTranscript(ctx, "https://www.youtube.com/watch?v="+videoID, "en")
	if err != nil {
		logger.Warn("Transcript extraction failed, using empty transcript",
			zap.String("video_id", videoID),
			zap.Error(err))
		transcript = ""
	}

	highlights, err := m.findHighlightsV3(ctx, info.Title, transcript)
	if err != nil {
		logger.Warn("Gemma highlight extraction failed, using fallback",
			zap.String("video_id", videoID),
			zap.Error(err))
		highlights = m.fallbackHighlights(transcript)
	}

	if len(highlights) == 0 {
		return fmt.Errorf("no highlights found (Gemma and fallback both failed)")
	}

	classification, err := m.classifyWithGemma(ctx, info)
	if err != nil {
		logger.Warn("Classification failed, using default",
			zap.String("video_id", videoID),
			zap.Error(err))
		classification = &GemmaResult{Category: "General", Reason: "Fallback due to error"}
	}

	successCount := 0
	for i, h := range highlights {
		clipFile := fmt.Sprintf("%s/clip_%s_%d.mp4", os.TempDir(), videoID, i+1)

		if err := m.downloadPreciseClip(ctx, videoID, h.StartSec, h.Duration, clipFile); err != nil {
			logger.Warn("Clip download failed, skipping",
				zap.String("video_id", videoID),
				zap.Int("segment", i+1),
				zap.Error(err))
			continue
		}

		folderID, folderPath, folderErr := m.resolveTargetFolder(ctx, classification.Category, info.Title)
		if folderErr != nil {
			logger.Warn("Folder resolution failed, using default",
				zap.String("video_id", videoID),
				zap.Error(folderErr))
			folderID = "DEFAULT_FOLDER"
			folderPath = "General/" + sanitizeFolderName(info.Title)
		}

		driveFileID, uploadErr := m.uploadToDrive(ctx, clipFile, folderID, folderPath)
		if uploadErr != nil {
			logger.Warn("Drive upload failed, skipping clip",
				zap.String("video_id", videoID),
				zap.Int("segment", i+1),
				zap.Error(uploadErr))
			continue
		}

		if err := m.saveClipToDB(ctx, videoID, driveFileID, folderID, folderPath, h, classification); err != nil {
			logger.Warn("Database save failed, but clip uploaded successfully",
				zap.String("video_id", videoID),
				zap.Error(err))
		}

		successCount++
		logger.Info("Clip processed successfully",
			zap.String("video_id", videoID),
			zap.Int("segment", i+1),
			zap.String("drive_id", driveFileID))

		_ = os.Remove(clipFile)
	}

	if successCount == 0 {
		return fmt.Errorf("no clips were successfully processed")
	}

	logger.Info("Video processing completed",
		zap.String("video_id", videoID),
		zap.Int("clips_processed", successCount),
		zap.Int("total_highlights", len(highlights)))

	return nil
}
