package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

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
		"--print", "%(id)s\x1f%(title)s\x1f%(channel)s\x1f%(channel_id)s\x1f%(view_count)s\x1f%(duration)s\x1f%(upload_date)s\x1f%(thumbnail)s",
		searchQuery,
	}

	if dateAfter := mapUploadDateForYtDlp(opts.UploadDate); dateAfter != "" {
		args = append(args, "--dateafter", dateAfter)
	}
	if durationFilter := mapDurationForYtDlp(opts.Duration); durationFilter != "" {
		args = append(args, "--match-filter", durationFilter)
	}

	cmd := exec.CommandContext(ctx, b.config.YtDlpPath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp search failed: %w", err)
	}

	results := b.parseSearchOutput(string(output))
	sortSearchResults(results, opts.SortBy)
	return results, nil
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
		"--print", "%(id)s\x1f%(title)s\x1f%(channel)s\x1f%(channel_id)s\x1f%(view_count)s\x1f%(duration)s\x1f%(upload_date)s\x1f%(thumbnail)s",
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
		"--print", "%(id)s\x1f%(title)s\x1f%(channel)s\x1f%(channel_id)s\x1f%(view_count)s\x1f%(duration)s\x1f%(upload_date)s\x1f%(thumbnail)s",
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
