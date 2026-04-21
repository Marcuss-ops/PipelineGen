package channelmonitor

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

func (m *V3Monitor) saveClipToDB(ctx context.Context, videoID, driveID, folderID, path string, h Highlight, g *GemmaResult) error {
	if m.db == nil {
		logger.Warn("Database not configured, skipping save")
		return nil
	}

	query := `
	INSERT INTO clips (video_id, drive_id, folder_id, folder_path, start_sec, duration, reason, category, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(video_id) DO UPDATE SET
		updated_at = CURRENT_TIMESTAMP
	`

	category := "Unknown"
	if g != nil {
		category = g.Category
	}

	err := m.db.QueryRowContext(ctx, query,
		videoID,
		driveID,
		folderID,
		path,
		h.StartSec,
		h.Duration,
		h.Reason,
		category,
		time.Now()).Err()
	if err != nil {
		logger.Error("Failed to save clip to database",
			zap.String("video_id", videoID),
			zap.Error(err))
		return fmt.Errorf("database save failed: %w", err)
	}

	logger.Debug("Clip saved to database",
		zap.String("video_id", videoID),
		zap.String("drive_id", driveID))

	return nil
}

func (m *V3Monitor) videoExists(ctx context.Context, videoID string) bool {
	if m.db == nil {
		return false
	}
	var count int
	err := m.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM videos WHERE video_id = ?", videoID).Scan(&count)
	return err == nil && count > 0
}

func (m *V3Monitor) saveVideoMetadata(ctx context.Context, info interface{}, classification *GemmaResult) error {
	return nil
}

func (m *V3Monitor) updateLastChecked(ctx context.Context, channelID string) error {
	return nil
}

func (m *V3Monitor) queueDownloadJob(ctx context.Context, info interface{}, classification *GemmaResult) {
}
