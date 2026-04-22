package channelmonitor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// processChannel processes one YouTube channel
func (m *Monitor) processChannel(ctx context.Context, ch ChannelConfig) ([]VideoResult, error) {
	logger.Info("Processing channel", zap.String("url", ch.URL))

	cb := GetCircuitBreaker(ch.URL)
	if !cb.Allow() {
		logger.Warn("Circuit open, skipping channel",
			zap.String("channel", ch.URL),
			zap.String("state", cb.State().String()),
		)
		return nil, nil
	}

	videos, err := m.ytClient.GetChannelVideos(ctx, ch.URL, &youtube.ChannelOptions{
		Limit: 50,
	})
	if err != nil {
		cb.RecordFailure()
		return nil, fmt.Errorf("failed to get channel videos: %w", err)
	}

	if len(videos) == 0 {
		logger.Info("No videos found for channel", zap.String("url", ch.URL))
		return nil, nil
	}

	// Filter by configured timeframe (default month).
	timeframe := normalizeVideoTimeframe(m.config.VideoTimeframe)
	windowStart := timeframeStart(time.Now().UTC(), timeframe)
	var windowVideos []youtube.SearchResult
	var filtered int
	for _, v := range videos {
		if isWithinTimeframe(v, windowStart) {
			windowVideos = append(windowVideos, v)
		} else {
			filtered++
		}
	}

	if len(windowVideos) == 0 {
		logger.Info("No videos found in timeframe",
			zap.String("channel", ch.URL),
			zap.String("timeframe", timeframe),
			zap.Int("filtered_out", filtered),
		)
		return nil, nil
	}

	logger.Info("Found videos in timeframe",
		zap.String("channel", ch.URL),
		zap.Int("count", len(windowVideos)),
		zap.String("timeframe", timeframe),
		zap.String("window_start", windowStart.Format(time.RFC3339)),
	)

	// Sort by views descending
	sort.Slice(windowVideos, func(i, j int) bool {
		return windowVideos[i].Views > windowVideos[j].Views
	})

	// Process a bounded number of top videos in the selected timeframe.
	var results []VideoResult
	maxVideos := ch.MaxVideos
	if maxVideos <= 0 {
		maxVideos = 5
	}
	processed := 0
	filterEngine := m.filterEngine

	for _, v := range windowVideos {
		if processed >= maxVideos {
			break
		}

		// Skip Shorts
		if isShorts(v) {
			continue
		}

		// Use FilterEngine for unified filtering
		videoInfo := VideoInfo{
			ID:         v.ID,
			Title:      v.Title,
			Channel:    v.Channel,
			Views:      v.Views,
			Duration:   int(v.Duration.Seconds()),
			UploadDate: v.UploadDate,
		}
		filterResult := filterEngine.MatchVideo(videoInfo, ch)
		if !filterResult.Matched {
			logger.Debug("Video filtered out",
				zap.String("video_id", v.ID),
				zap.String("reason", filterResult.Reason),
			)
			continue
		}

		// Skip already processed
		if m.isProcessed(v.ID) {
			continue
		}

		logger.Info("Processing trending video",
			zap.String("title", v.Title),
			zap.String("id", v.ID),
			zap.Int64("views", v.Views),
			zap.Int("rank", processed+1),
		)

		// Extract transcript
		transcript, err := m.extractTranscript(ctx, v.ID)
		if err != nil {
			logger.Warn("Failed to extract transcript, skipping",
				zap.String("video_id", v.ID),
				zap.Error(err),
			)
			continue
		}

		if len(transcript) < 200 {
			logger.Info("Transcript too short, skipping",
				zap.String("video_id", v.ID),
			)
			continue
		}

		// Find interesting segments with timestamps
		highlights := m.findHighlights(transcript)
		if len(highlights) == 0 {
			logger.Info("No highlights found, skipping",
				zap.String("video_id", v.ID),
			)
			continue
		}

		// Determine folder from topic
		folderPath, folderID, folderExisted, decision, err := m.resolveFolder(ctx, ch, v.Title)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve folder: %w", err)
		}

		// Download clips and upload to Drive (max 5 clips)
		clips, err := m.downloadAndUploadClips(ctx, v, highlights, folderID, folderPath, folderExisted, ch.MaxClipDuration, decision, ch.MaxClips)
		if err != nil {
			return nil, fmt.Errorf("failed to download/upload clips: %w", err)
		}

		result := VideoResult{
			VideoID:    v.ID,
			Title:      v.Title,
			Channel:    v.Channel,
			Views:      v.Views,
			Transcript: transcript,
			Highlights: highlights,
			Clips:      clips,
			FolderPath: folderPath,
		}

		// Mark as processed
		m.markProcessed(ProcessedVideoEntry{
			VideoID:     v.ID,
			Title:       v.Title,
			Channel:     v.Channel,
			ProcessedAt: time.Now(),
			FolderPath:  folderPath,
			ClipsCount:  len(clips),
		})

		logger.Info("Video processed successfully",
			zap.String("title", v.Title),
			zap.Int("clips_uploaded", len(clips)),
		)

		results = append(results, result)
		processed++
	}

	if processed == 0 {
		logger.Info("No new videos to process (all already processed or below threshold)",
			zap.String("channel", ch.URL),
		)
		return nil, nil
	}

	logger.Info("Channel processing complete",
		zap.Int("videos_processed", processed),
		zap.Int("total_timeframe_videos", len(windowVideos)),
	)

	cb.RecordSuccess()
	return results, nil
}

func normalizeVideoTimeframe(tf string) string {
	switch strings.ToLower(strings.TrimSpace(tf)) {
	case "24h", "day", "today":
		return "24h"
	case "week", "7d":
		return "week"
	case "month", "30d":
		return "month"
	default:
		return "month"
	}
}

func isWithinTimeframe(v youtube.SearchResult, windowStart time.Time) bool {
	if v.UploadDate == "" || v.UploadDate == "NA" {
		return true
	}

	uploadDate, err := parseUploadDate(v.UploadDate)
	if err != nil {
		return true
	}
	return uploadDate.After(windowStart) || uploadDate.Equal(windowStart)
}

func parseUploadDate(raw string) (time.Time, error) {
	dateStr := strings.TrimSpace(raw)
	dateStr = strings.ReplaceAll(dateStr, "T", "")
	dateStr = strings.ReplaceAll(dateStr, "-", "")
	dateStr = strings.ReplaceAll(dateStr, "Z", "")

	if len(dateStr) < 8 {
		return time.Time{}, errors.New("invalid upload date: " + raw)
	}
	return time.Parse("20060102", dateStr[:8])
}

func timeframeStart(now time.Time, tf string) time.Time {
	switch tf {
	case "24h":
		return now.Add(-24 * time.Hour)
	case "week", "7d":
		return now.Add(-7 * 24 * time.Hour)
	case "month", "30d":
		return now.Add(-30 * 24 * time.Hour)
	default:
		return now.Add(-30 * 24 * time.Hour)
	}
}

// isShorts checks if a video is a YouTube Short
func isShorts(v youtube.SearchResult) bool {
	return strings.Contains(strings.ToLower(v.Title), "#shorts") ||
		strings.Contains(strings.ToLower(v.Title), "| short")
}
