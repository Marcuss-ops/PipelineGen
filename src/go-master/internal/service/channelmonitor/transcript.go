package channelmonitor

import (
	"context"
	"fmt"
	"time"

	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// extractTranscript extracts transcript from a YouTube video using yt-dlp
func (m *Monitor) extractTranscript(ctx context.Context, videoID string) (string, error) {
	url := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
	logger.Info("Extracting transcript from YouTube video",
		zap.String("video_id", videoID),
		zap.String("language", "en"),
	)

	// Preserve caller cancellation and impose a bounded transcript timeout.
	transcriptCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	if m.ytClient == nil {
		return "", fmt.Errorf("youtube client not configured")
	}
	transcript, err := m.ytClient.GetTranscript(transcriptCtx, url, "en")
	if err != nil {
		return "", fmt.Errorf("no transcript available for video %s: %w", videoID, err)
	}

	return transcript, nil
}
