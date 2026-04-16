// Package youtube provides a unified YouTube client interface
package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// YtDlpBackend implements Client using yt-dlp subprocess
type YtDlpBackend struct {
	config *Config
}

// NewYtDlpBackend creates a new yt-dlp backend client
func NewYtDlpBackend(config *Config) *YtDlpBackend {
	if config == nil {
		config = &Config{
			Backend:              "ytdlp",
			YtDlpPath:           "yt-dlp",
			FFmpegPath:          "ffmpeg",
			DefaultFormat:       "best[ext=mp4]/best",
			DefaultMaxHeight:    1080,
			DefaultRetries:      3,
			MaxConcurrentDownloads: 5,
			GPUAcceleration:     false,
		}
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

// Search searches YouTube for videos
func (b *YtDlpBackend) Search(ctx context.Context, query string, opts *SearchOptions) ([]SearchResult, error) {
	if opts == nil {
		opts = &SearchOptions{MaxResults: 10}
	}
	if opts.MaxResults <= 0 {
		opts.MaxResults = 10
	}
	if opts.MaxResults > 50 {
		opts.MaxResults = 50
	}
	
	searchQuery := fmt.Sprintf("ytsearch%d:%s", opts.MaxResults, query)
	
	args := []string{
		"--flat-playlist",
		"--print", "%(id)s|%(title)s|%(channel)s|%(channel_id)s|%(view_count)s|%(duration)s|%(upload_date)s|%(thumbnail)s",
		searchQuery,
	}
	
	if opts.SortBy != "" {
		args = append(args, "--sort", opts.SortBy)
	}
	
	cmd := exec.CommandContext(ctx, b.config.YtDlpPath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp search failed: %w", err)
	}
	
	return b.parseSearchOutput(string(output)), nil
}

// GetChannelVideos gets videos from a specific channel
func (b *YtDlpBackend) GetChannelVideos(ctx context.Context, channelURL string, opts *ChannelOptions) ([]SearchResult, error) {
	if opts == nil {
		opts = &ChannelOptions{Limit: 10}
	}
	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	
	args := []string{
		"--flat-playlist",
		"--print", "%(id)s|%(title)s|%(channel)s|%(channel_id)s|%(view_count)s|%(duration)s|%(upload_date)s|%(thumbnail)s",
		channelURL,
		"--playlist-end", fmt.Sprintf("%d", opts.Limit),
	}
	
	cmd := exec.CommandContext(ctx, b.config.YtDlpPath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp channel videos failed: %w", err)
	}
	
	return b.parseSearchOutput(string(output)), nil
}

// GetTrending gets trending videos for a region
func (b *YtDlpBackend) GetTrending(ctx context.Context, region string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	
	trendingURL := fmt.Sprintf("https://www.youtube.com/feed/trending?region=%s", region)
	
	args := []string{
		"--flat-playlist",
		"--print", "%(id)s|%(title)s|%(channel)s|%(channel_id)s|%(view_count)s|%(duration)s|%(upload_date)s|%(thumbnail)s",
		trendingURL,
		"--playlist-end", fmt.Sprintf("%d", limit),
	}
	
	cmd := exec.CommandContext(ctx, b.config.YtDlpPath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp trending failed: %w", err)
	}
	
	return b.parseSearchOutput(string(output)), nil
}

// GetSubtitles extracts subtitles for a video
func (b *YtDlpBackend) GetSubtitles(ctx context.Context, videoID string, lang string) (*SubtitleInfo, error) {
	if lang == "" {
		lang = "en"
	}

	url := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	args := []string{
		"--skip-download",
		"--write-sub",
		"--write-auto-sub",
		"--sub-lang", lang,
		"--sub-format", "vtt",
		"--convert-subs", "vtt",
		"--output", fmt.Sprintf("/tmp/velox/subs/%s", videoID),
		url,
	}

	cmd := exec.CommandContext(ctx, b.config.YtDlpPath, args...)
	_, err := cmd.CombinedOutput()
	if err != nil {
		// Try to find the subtitle file even if command fails
		// yt-dlp might fail to download video but succeed in extracting subtitles
	}

	// Read the VTT file
	vttPath := fmt.Sprintf("/tmp/velox/subs/%s.%s.vtt", videoID, lang)
	content, err := readFile(vttPath)
	if err != nil {
		// Try alternative language or auto-generated
		vttPath = fmt.Sprintf("/tmp/velox/subs/%s.en.vtt", videoID)
		content, err = readFile(vttPath)
		if err != nil {
			return nil, fmt.Errorf("subtitles not available: %w", err)
		}
		lang = "en"
	}

	return &SubtitleInfo{
		VideoID:    videoID,
		Language:   lang,
		VTTContent: string(content),
	}, nil
}

// GetTranscript extracts transcript (subtitles) from a YouTube video URL
func (b *YtDlpBackend) GetTranscript(ctx context.Context, url string, lang string) (string, error) {
	videoID := extractVideoID(url)
	if videoID == "" {
		return "", fmt.Errorf("could not extract video ID from URL: %s", url)
	}

	if lang == "" {
		lang = "en"
	}

	logger.Info("Extracting transcript from YouTube video",
		zap.String("video_id", videoID),
		zap.String("language", lang),
	)

	// Create temp directory
	tempDir := fmt.Sprintf("/tmp/velox/transcripts/%s", videoID)
	if err := ensureDirs(tempDir); err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	// Try to download subtitles
	args := []string{
		"--skip-download",
		"--write-sub",
		"--write-auto-sub",
		"--sub-lang", lang,
		"--sub-format", "vtt",
		"--convert-subs", "vtt",
		"--output", fmt.Sprintf("%s/%s", tempDir, videoID),
		url,
	}

	cmd := exec.CommandContext(ctx, b.config.YtDlpPath, args...)
	_, err := cmd.CombinedOutput()
	if err != nil {
		logger.Debug("Subtitle download failed, trying alternative languages",
			zap.Error(err),
		)
	}

	// Try to read VTT file
	vttPath := fmt.Sprintf("%s/%s.%s.vtt", tempDir, videoID, lang)
	content, err := readFile(vttPath)
	if err != nil {
		// Try English as fallback
		if lang != "en" {
			vttPath = fmt.Sprintf("%s/%s.en.vtt", tempDir, videoID)
			content, err = readFile(vttPath)
			if err == nil {
				lang = "en"
			}
		}

		if err != nil {
			return "", fmt.Errorf("transcript not available: no subtitles found")
		}
	}

	// Convert VTT to plain text
	text := b.vttToText(string(content))

	logger.Info("Transcript extracted successfully",
		zap.String("video_id", videoID),
		zap.String("language", lang),
		zap.Int("length", len(text)),
	)

	return text, nil
}

// vttToText converts VTT subtitle format to plain text
func (b *YtDlpBackend) vttToText(vttContent string) string {
	lines := strings.Split(vttContent, "\n")
	var textParts []string

	for _, line := range lines {
		// Skip VTT metadata and timestamps
		if strings.Contains(line, "WEBVTT") ||
			strings.Contains(line, "-->") ||
			strings.TrimSpace(line) == "" {
			continue
		}

		// Skip line numbers
		if _, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
			continue
		}

		// Clean HTML tags
		line = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(line, "")
		line = strings.TrimSpace(line)

		if line != "" {
			textParts = append(textParts, line)
		}
	}

	return strings.Join(textParts, " ")
}

// CheckAvailable verifies that yt-dlp is installed and working
func (b *YtDlpBackend) CheckAvailable(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, b.config.YtDlpPath, "--version")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("yt-dlp not available: %w", err)
	}
	
	logger.Info("yt-dlp is available", zap.String("version", strings.TrimSpace(string(output))))
	return nil
}

// Helper methods

type downloadResult struct {
	FilePath string
	FileSize int64
}

func (b *YtDlpBackend) downloadOnce(ctx context.Context, url, outputPath, format, cookiesFile, proxy string, progress ProgressCallback) (*downloadResult, error) {
	args := []string{
		"--format", format,
		"--output", outputPath,
		"--no-playlist",
		"--restrict-filenames",
		"--no-warnings",
	}
	
	if cookiesFile != "" {
		args = append(args, "--cookies", cookiesFile)
	} else if b.config.DefaultCookiesFile != "" {
		args = append(args, "--cookies", b.config.DefaultCookiesFile)
	}
	
	if proxy != "" {
		args = append(args, "--proxy", proxy)
	} else if b.config.Proxy != "" {
		args = append(args, "--proxy", b.config.Proxy)
	}
	
	// Add progress flag if callback provided
	if progress != nil {
		args = append(args, "--newline")
	}
	
	args = append(args, url)
	
	cmd := exec.CommandContext(ctx, b.config.YtDlpPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp download failed: %w\n%s", err, string(output))
	}
	
	// Find the downloaded file
	files, err := findDownloadedFiles(outputPath)
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("could not find downloaded video file")
	}
	
	filePath := files[0]
	fileSize := getFileSize(filePath)
	
	return &downloadResult{
		FilePath: filePath,
		FileSize: fileSize,
	}, nil
}

func (b *YtDlpBackend) downloadAudioOnce(ctx context.Context, url, outputPath, format, audioFormat, cookiesFile, proxy string, progress ProgressCallback) (*downloadResult, error) {
	args := []string{
		"--format", format,
		"--output", outputPath,
		"--no-playlist",
		"--extract-audio",
		"--audio-format", audioFormat,
	}
	
	if cookiesFile != "" {
		args = append(args, "--cookies", cookiesFile)
	} else if b.config.DefaultCookiesFile != "" {
		args = append(args, "--cookies", b.config.DefaultCookiesFile)
	}
	
	if proxy != "" {
		args = append(args, "--proxy", proxy)
	} else if b.config.Proxy != "" {
		args = append(args, "--proxy", b.config.Proxy)
	}
	
	args = append(args, url)
	
	cmd := exec.CommandContext(ctx, b.config.YtDlpPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp audio download failed: %w\n%s", err, string(output))
	}
	
	files, err := findDownloadedFiles(outputPath)
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("could not find downloaded audio file")
	}
	
	filePath := files[0]
	fileSize := getFileSize(filePath)
	
	return &downloadResult{
		FilePath: filePath,
		FileSize: fileSize,
	}, nil
}

func (b *YtDlpBackend) getVideoInfo(ctx context.Context, url string) (*VideoInfo, error) {
	args := []string{
		"--dump-json",
		"--no-playlist",
		"--no-warnings",
		url,
	}
	
	cmd := exec.CommandContext(ctx, b.config.YtDlpPath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp info failed: %w", err)
	}
	
	var info VideoInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return nil, fmt.Errorf("failed to parse video info: %w", err)
	}
	
	return &info, nil
}

func (b *YtDlpBackend) buildFormatString(req *DownloadRequest) string {
	if req.Format != "" {
		return req.Format
	}
	
	if req.MaxHeight > 0 {
		return fmt.Sprintf("best[height<=%d][ext=mp4]/best[height<=%d]", req.MaxHeight, req.MaxHeight)
	}
	
	if b.config.DefaultMaxHeight > 0 {
		return fmt.Sprintf("best[height<=%d][ext=mp4]/best", b.config.DefaultMaxHeight)
	}
	
	return b.config.DefaultFormat
}

func (b *YtDlpBackend) parseSearchOutput(output string) []SearchResult {
	var results []SearchResult
	
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		
		parts := strings.Split(line, "|")
		if len(parts) < 8 {
			continue
		}
		
		viewCount, _ := strconv.ParseInt(parts[4], 10, 64)
		durationSec, _ := strconv.Atoi(parts[5])
		
		results = append(results, SearchResult{
			ID:         parts[0],
			Title:      parts[1],
			Channel:    parts[2],
			ChannelID:  parts[3],
			Views:      viewCount,
			Duration:   time.Duration(durationSec) * time.Second,
			UploadDate: parts[6],
			Thumbnail:  parts[7],
			URL:        fmt.Sprintf("https://www.youtube.com/watch?v=%s", parts[0]),
		})
	}
	
	return results
}

// Utility functions

func extractVideoID(url string) string {
	// Handle various YouTube URL formats
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?:v=|/v/|/embed/|youtu\.be/)([a-zA-Z0-9_-]{11})`),
		regexp.MustCompile(`youtube\.com/watch\?v=([a-zA-Z0-9_-]{11})`),
	}
	
	for _, pattern := range patterns {
		matches := pattern.FindStringSubmatch(url)
		if len(matches) > 1 {
			return matches[1]
		}
	}
	
	return ""
}

func findDownloadedFiles(pattern string) ([]string, error) {
	// Try common extensions
	extensions := []string{".mp4", ".webm", ".mkv", ".m4a", ".mp3", ".opus"}
	var files []string
	
	for _, ext := range extensions {
		basePath := strings.TrimSuffix(pattern, ".%(ext)s")
		filePath := basePath + ext
		if _, err := exec.LookPath(filePath); err == nil {
			files = append(files, filePath)
		}
	}
	
	// Fallback: use glob pattern matching
	// Note: This requires implementing proper glob matching
	// For now, return what we found
	return files, nil
}

func getFileSize(path string) int64 {
	info, err := exec.Command("stat", "-c", "%s", path).Output()
	if err != nil {
		return 0
	}
	
	size, _ := strconv.ParseInt(strings.TrimSpace(string(info)), 10, 64)
	return size
}

func readFile(path string) ([]byte, error) {
	return exec.Command("cat", path).Output()
}
