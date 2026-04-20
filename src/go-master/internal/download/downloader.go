// Package download provides unified video download functionality
// supporting YouTube and TikTok.
package download

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/security"
)

// Platform represents a video source platform
type Platform string

const (
	PlatformYouTube Platform = "youtube"
	PlatformTikTok  Platform = "tiktok"
)

// DownloadResult represents the result of a video download
type DownloadResult struct {
	Platform  Platform
	VideoID   string
	Title     string
	FilePath  string
	Duration  float64
	Thumbnail string
	Author    string
}

// Downloader handles video downloads from various platforms
type Downloader struct {
	outputDir string
}

// NewDownloader creates a new video downloader
func NewDownloader(outputDir string) *Downloader {
	if outputDir == "" {
		outputDir = "/tmp/velox/downloads"
	}
	os.MkdirAll(outputDir, 0755)
	return &Downloader{outputDir: outputDir}
}

// Download downloads a video from the given URL
func (d *Downloader) Download(ctx context.Context, rawURL string) (*DownloadResult, error) {
	// Validate URL before passing to yt-dlp
	if err := security.ValidateDownloadURL(rawURL); err != nil {
		return nil, fmt.Errorf("invalid download URL: %w", err)
	}

	platform := DetectPlatform(rawURL)
	if platform == "" {
		return nil, fmt.Errorf("unsupported URL: %s. Supported platforms: YouTube, TikTok", rawURL)
	}

	switch platform {
	case PlatformYouTube:
		return d.downloadYouTube(ctx, rawURL)
	case PlatformTikTok:
		return d.downloadTikTok(ctx, rawURL)
	default:
		return nil, fmt.Errorf("platform not supported: %s", platform)
	}
}

// DetectPlatform detects the video platform from URL
func DetectPlatform(url string) Platform {
	lower := strings.ToLower(url)
	if isYouTubeURL(lower) {
		return PlatformYouTube
	}
	if isTikTokURL(lower) {
		return PlatformTikTok
	}
	return ""
}

// ExtractVideoID extracts video ID from URL
func ExtractVideoID(url string) string {
	if isYouTubeURL(strings.ToLower(url)) {
		return extractYouTubeID(url)
	}
	if isTikTokURL(strings.ToLower(url)) {
		return extractTikTokID(url)
	}
	return ""
}

func isYouTubeURL(url string) bool {
	return strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be")
}

func isTikTokURL(url string) bool {
	return strings.Contains(url, "tiktok.com")
}

func extractYouTubeID(url string) string {
	// Handle various YouTube URL formats
	if strings.Contains(url, "v=") {
		// https://www.youtube.com/watch?v=VIDEO_ID
		parts := strings.Split(url, "v=")
		if len(parts) > 1 {
			return strings.Split(parts[1], "&")[0]
		}
	}
	if strings.Contains(url, "youtu.be/") {
		// https://youtu.be/VIDEO_ID
		parts := strings.Split(url, "youtu.be/")
		if len(parts) > 1 {
			return strings.Split(parts[1], "?")[0]
		}
	}
	if strings.Contains(url, "embed/") {
		// https://www.youtube.com/embed/VIDEO_ID
		parts := strings.Split(url, "embed/")
		if len(parts) > 1 {
			return strings.Split(parts[1], "?")[0]
		}
	}
	return ""
}

func extractTikTokID(url string) string {
	// TikTok URLs: https://www.tiktok.com/@user/video/VIDEO_ID
	re := regexp.MustCompile(`/video/(\d+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1]
	}
	// Short URLs: https://vm.tiktok.com/VIDEO_ID
	parts := strings.Split(url, "/")
	return parts[len(parts)-1]
}

// downloadYouTube downloads a video using yt-dlp
func (d *Downloader) downloadYouTube(ctx context.Context, url string) (*DownloadResult, error) {
	videoID := extractYouTubeID(url)
	if videoID == "" {
		return nil, fmt.Errorf("could not extract video ID from YouTube URL")
	}

	logger.Info("Downloading YouTube video",
		zap.String("url", url),
		zap.String("video_id", videoID),
	)

	// Get video info first
	info, err := d.getVideoInfo(ctx, url)
	if err != nil {
		logger.Warn("Failed to get video info, continuing with download",
			zap.Error(err),
		)
		info = &VideoInfo{
			ID:    videoID,
			Title: videoID,
		}
	}

	// Download the video
	outputPath := filepath.Join(d.outputDir, "youtube", videoID, "%(title)s.%(ext)s")
	os.MkdirAll(filepath.Join(d.outputDir, "youtube", videoID), 0755)

	args := []string{
		"--no-playlist",
		"--format", "best[ext=mp4]/best",
		"--output", outputPath,
		"--restrict-filenames",
		"--no-warnings",
	}
	args = append(args, youtube.BuildYtDlpAuthArgs("", "")...)

	var lastErr error
	for _, extractorArgs := range youtube.YouTubeExtractorArgsVariants() {
		attemptArgs := append([]string{}, args...)
		if extractorArgs != "" {
			attemptArgs = append(attemptArgs, "--extractor-args", extractorArgs)
		}
		attemptArgs = append(attemptArgs, url)

		cmd := exec.CommandContext(ctx, "yt-dlp", attemptArgs...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			lastErr = fmt.Errorf("yt-dlp download failed (extractor-args=%q): %w\n%s", extractorArgs, err, string(output))
			continue
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		return nil, lastErr
	}

	// Find downloaded file
	files, err := filepath.Glob(filepath.Join(d.outputDir, "youtube", videoID, "*"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("could not find downloaded video file")
	}

	result := &DownloadResult{
		Platform:  PlatformYouTube,
		VideoID:   videoID,
		Title:     info.Title,
		FilePath:  files[0],
		Duration:  info.Duration,
		Thumbnail: info.Thumbnail,
		Author:    info.Uploader,
	}

	logger.Info("YouTube video downloaded successfully",
		zap.String("video_id", videoID),
		zap.String("title", result.Title),
		zap.String("file", result.FilePath),
	)

	return result, nil
}

// downloadTikTok downloads a TikTok video using yt-dlp
func (d *Downloader) downloadTikTok(ctx context.Context, url string) (*DownloadResult, error) {
	videoID := extractTikTokID(url)
	if videoID == "" {
		return nil, fmt.Errorf("could not extract video ID from TikTok URL")
	}

	logger.Info("Downloading TikTok video",
		zap.String("url", url),
		zap.String("video_id", videoID),
	)

	// Get video info first
	info, err := d.getVideoInfo(ctx, url)
	if err != nil {
		logger.Warn("Failed to get video info, continuing with download",
			zap.Error(err),
		)
		info = &VideoInfo{
			ID:    videoID,
			Title: videoID,
		}
	}

	// Download the video
	outputPath := filepath.Join(d.outputDir, "tiktok", videoID, "%(title)s.%(ext)s")
	os.MkdirAll(filepath.Join(d.outputDir, "tiktok", videoID), 0755)

	args := []string{
		"--no-playlist",
		"--format", "best[ext=mp4]/best",
		"--output", outputPath,
		"--restrict-filenames",
		"--no-warnings",
		url,
	}

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp download failed: %w\n%s", err, string(output))
	}

	// Find downloaded file
	files, err := filepath.Glob(filepath.Join(d.outputDir, "tiktok", videoID, "*"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("could not find downloaded video file")
	}

	result := &DownloadResult{
		Platform:  PlatformTikTok,
		VideoID:   videoID,
		Title:     info.Title,
		FilePath:  files[0],
		Duration:  info.Duration,
		Thumbnail: info.Thumbnail,
		Author:    info.Uploader,
	}

	logger.Info("TikTok video downloaded successfully",
		zap.String("video_id", videoID),
		zap.String("title", result.Title),
		zap.String("file", result.FilePath),
	)

	return result, nil
}

// VideoInfo represents metadata about a video
type VideoInfo struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Duration  float64 `json:"duration"`
	Uploader  string  `json:"uploader"`
	Thumbnail string  `json:"thumbnail"`
}

// getVideoInfo fetches video metadata without downloading
func (d *Downloader) getVideoInfo(ctx context.Context, url string) (*VideoInfo, error) {
	baseArgs := []string{
		"--dump-json",
		"--no-download",
		"--no-warnings",
	}
	baseArgs = append(baseArgs, youtube.BuildYtDlpAuthArgs("", "")...)

	var output []byte
	var lastErr error
	for _, extractorArgs := range youtube.YouTubeExtractorArgsVariants() {
		args := append([]string{}, baseArgs...)
		if extractorArgs != "" {
			args = append(args, "--extractor-args", extractorArgs)
		}
		args = append(args, url)

		cmd := exec.CommandContext(ctx, "yt-dlp", args...)
		attemptOutput, err := cmd.CombinedOutput()
		if err != nil {
			lastErr = fmt.Errorf("failed to get video info (extractor-args=%q): %w", extractorArgs, err)
			continue
		}
		output = attemptOutput
		lastErr = nil
		break
	}
	if lastErr != nil {
		return nil, lastErr
	}

	var info VideoInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return nil, fmt.Errorf("failed to parse video info: %w", err)
	}

	return &info, nil
}

// ListDownloads lists all downloaded videos organized by platform
func (d *Downloader) ListDownloads() (map[Platform][]string, error) {
	result := make(map[Platform][]string)

	platforms := []struct {
		path string
		plat Platform
	}{
		{filepath.Join(d.outputDir, "youtube"), PlatformYouTube},
		{filepath.Join(d.outputDir, "tiktok"), PlatformTikTok},
	}

	for _, p := range platforms {
		if _, err := os.Stat(p.path); os.IsNotExist(err) {
			result[p.plat] = []string{}
			continue
		}

		files, err := filepath.Glob(filepath.Join(p.path, "*", "*"))
		if err != nil {
			result[p.plat] = []string{}
			continue
		}

		result[p.plat] = files
	}

	return result, nil
}

// GetPlatformFolder returns the folder path for a platform
func (d *Downloader) GetPlatformFolder(platform Platform) string {
	return filepath.Join(d.outputDir, string(platform))
}
