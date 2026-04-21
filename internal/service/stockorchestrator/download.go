package stockorchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/internal/stock"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

func (s *StockOrchestratorService) downloadVideos(ctx context.Context, results []stock.VideoResult, quality string) ([]DownloadedVideo, []error) {
	downloaded := make([]DownloadedVideo, 0, len(results))
	var errs []error

	for _, result := range results {
		video, err := s.downloadSingleVideo(ctx, result.URL, quality)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to download %s: %w", result.URL, err))
			logger.Warn("Failed to download video",
				zap.String("url", result.URL),
				zap.Error(err),
			)
			continue
		}
		downloaded = append(downloaded, *video)
	}

	return downloaded, errs
}

// downloadSingleVideo downloads a single video
func (s *StockOrchestratorService) downloadSingleVideo(ctx context.Context, url string, quality string) (*DownloadedVideo, error) {
	// Build output filename from URL
	filename := fmt.Sprintf("stock_%d_%%(id)s.%%(ext)s", time.Now().Unix())
	outputTemplate := filepath.Join(s.downloadDir, filename)

	// Build format string
	formatStr := "bestvideo[height<=1080]+bestaudio/best[height<=1080]"
	if quality == "720p" {
		formatStr = "bestvideo[height<=720]+bestaudio/best[height<=720]"
	} else if quality == "4k" {
		formatStr = "bestvideo[height<=2160]+bestaudio/best[height<=2160]"
	}

	// Execute yt-dlp
	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--newline",
		"--no-warnings",
		"--no-playlist",
		"-f", formatStr,
		"-o", outputTemplate,
		"--dump-json",
		url,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp failed: %v", err)
	}

	// Parse JSON metadata from yt-dlp output (last line)
	lines := strings.Split(string(output), "\n")
	var metadata map[string]interface{}
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "{") {
			// Try to parse as JSON
			if err := parseJSON(line, &metadata); err == nil {
				break
			}
		}
	}

	// Find the downloaded file
	files, err := filepath.Glob(filepath.Join(s.downloadDir, "stock_*"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("no downloaded file found")
	}

	localPath := files[len(files)-1] // Most recent file
	fileInfo, err := os.Stat(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	// Extract metadata
	videoID := ""
	title := ""
	duration := 0
	resolution := ""

	if metadata != nil {
		if id, ok := metadata["id"].(string); ok {
			videoID = id
		}
		if t, ok := metadata["title"].(string); ok {
			title = t
		}
		if d, ok := metadata["duration"].(float64); ok {
			duration = int(d)
		}
		if h, ok := metadata["height"].(float64); ok {
			if w, ok := metadata["width"].(float64); ok {
				resolution = fmt.Sprintf("%.0fx%.0f", w, h)
			}
		}
	}

	if videoID == "" {
		videoID = fmt.Sprintf("unknown_%d", time.Now().Unix())
	}

	return &DownloadedVideo{
		VideoID:    videoID,
		Title:      title,
		LocalPath:  localPath,
		FileSize:   fileInfo.Size(),
		Duration:   duration,
		Resolution: resolution,
		YouTubeURL: url,
	}, nil
}

// extractEntitiesFromVideos extracts entities from video titles/descriptions
