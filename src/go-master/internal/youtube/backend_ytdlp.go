// Package youtube provides a unified YouTube client interface
package youtube

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// YtDlpBackend implements Client using yt-dlp subprocess
type YtDlpBackend struct {
	config *Config
}

const searchPrintDelimiter = "\x1f"

// NewYtDlpBackend creates a new yt-dlp backend client
func NewYtDlpBackend(config *Config) *YtDlpBackend {
	if config == nil {
		config = DefaultConfig()
	}
	return &YtDlpBackend{config: config}
}

// GetVideo fetches video metadata without downloading
func (b *YtDlpBackend) GetVideo(ctx context.Context, videoID string) (*VideoInfo, error) {
	url := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
	return b.getVideoInfo(ctx, url)
}

// Download downloads a video with the given parameters
func (b *YtDlpBackend) Download(ctx context.Context, req *DownloadRequest) (*DownloadResult, error) {
	if req.Retries <= 0 {
		req.Retries = b.config.DefaultRetries
	}
	if req.OutputDir == "" {
		req.OutputDir = "/tmp/velox/downloads/youtube"
	}

	// Extract video ID if not provided
	if req.VideoID == "" {
		req.VideoID = extractVideoID(req.URL)
		if req.VideoID == "" {
			return nil, fmt.Errorf("could not extract video ID from URL: %s", req.URL)
		}
	}

	// Set output filename if not provided
	if req.OutputFile == "" {
		req.OutputFile = req.VideoID
	}

	// Build format string
	formatStr := b.buildFormatString(req)

	// Build output path
	outputPath := fmt.Sprintf("%s/%s.%%(ext)s", req.OutputDir, req.OutputFile)

	var lastErr error
	for attempt := 1; attempt <= req.Retries; attempt++ {
		logger.Info("Downloading YouTube video",
			zap.String("url", req.URL),
			zap.String("video_id", req.VideoID),
			zap.Int("attempt", attempt),
			zap.Int("max_retries", req.Retries),
		)

		result, err := b.downloadOnce(ctx, req.URL, outputPath, formatStr, req.CookiesFile, req.Proxy, req.Progress)
		if err == nil {
			// Build result
			info, _ := b.getVideoInfo(ctx, req.URL)
			title := req.VideoID
			thumbnail := ""
			author := ""
			duration := time.Duration(0)

			if info != nil {
				title = info.Title
				author = info.Channel
				duration = info.Duration
				if len(info.Thumbnails) > 0 {
					thumbnail = info.Thumbnails[len(info.Thumbnails)-1].URL
				}
			}

			return &DownloadResult{
				VideoID:   req.VideoID,
				Title:     title,
				FilePath:  result.FilePath,
				FileSize:  result.FileSize,
				Duration:  duration,
				Thumbnail: thumbnail,
				Author:    author,
				Format:    formatStr,
			}, nil
		}

		lastErr = err
		logger.Warn("Download attempt failed",
			zap.String("video_id", req.VideoID),
			zap.Int("attempt", attempt),
			zap.Error(err),
		)

		// Wait before retry (exponential backoff)
		if attempt < req.Retries {
			waitTime := time.Duration(attempt) * 2 * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(waitTime):
			}
		}
	}

	return nil, fmt.Errorf("download failed after %d attempts: %w", req.Retries, lastErr)
}

// DownloadAudio downloads audio only from a video
func (b *YtDlpBackend) DownloadAudio(ctx context.Context, req *AudioDownloadRequest) (*AudioDownloadResult, error) {
	if req.Retries <= 0 {
		req.Retries = b.config.DefaultRetries
	}
	if req.OutputDir == "" {
		req.OutputDir = "/tmp/velox/downloads/audio"
	}
	if req.AudioFormat == "" {
		req.AudioFormat = "mp3"
	}

	// Extract video ID if not provided
	if req.VideoID == "" {
		req.VideoID = extractVideoID(req.URL)
		if req.VideoID == "" {
			return nil, fmt.Errorf("could not extract video ID from URL: %s", req.URL)
		}
	}

	if req.OutputFile == "" {
		req.OutputFile = req.VideoID
	}

	outputPath := fmt.Sprintf("%s/%s.%%(ext)s", req.OutputDir, req.OutputFile)

	// Build audio format selector
	audioFormat := "bestaudio"
	if req.AudioQuality != "" {
		audioFormat = req.AudioQuality
	}

	var lastErr error
	for attempt := 1; attempt <= req.Retries; attempt++ {
		logger.Info("Downloading YouTube audio",
			zap.String("url", req.URL),
			zap.String("video_id", req.VideoID),
			zap.Int("attempt", attempt),
		)

		result, err := b.downloadAudioOnce(ctx, req.URL, outputPath, audioFormat, req.AudioFormat, req.CookiesFile, req.Proxy, req.Progress)
		if err == nil {
			info, _ := b.getVideoInfo(ctx, req.URL)
			title := req.VideoID
			duration := time.Duration(0)

			if info != nil {
				title = info.Title
				duration = info.Duration
			}

			return &AudioDownloadResult{
				VideoID:  req.VideoID,
				Title:    title,
				FilePath: result.FilePath,
				FileSize: result.FileSize,
				Duration: duration,
				Format:   req.AudioFormat,
			}, nil
		}

		lastErr = err
		logger.Warn("Audio download attempt failed",
			zap.String("video_id", req.VideoID),
			zap.Int("attempt", attempt),
			zap.Error(err),
		)

		if attempt < req.Retries {
			waitTime := time.Duration(attempt) * 2 * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(waitTime):
			}
		}
	}

	return nil, fmt.Errorf("audio download failed after %d attempts: %w", req.Retries, lastErr)
}

// Helper methods

type downloadResult struct {
	FilePath string
	FileSize int64
}
