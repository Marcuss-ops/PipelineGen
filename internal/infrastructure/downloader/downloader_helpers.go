package downloader

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
)

// VideoInfo contains basic info from yt-dlp --flat-playlist.
type VideoInfo struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Views    int64   `json:"view_count"`
	Duration float64 `json:"duration"` // yt-dlp might return float
}

// YouTubeMetadata contains full metadata from yt-dlp --dump-json for a single video.
type YouTubeMetadata struct {
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	Description  string           `json:"description"`
	Tags         []string         `json:"tags"`
	Categories   []string         `json:"categories"`
	Language     string           `json:"language"`
	Uploader     string           `json:"uploader"`
	UploadDate   string           `json:"upload_date"`
	ViewCount    int64            `json:"view_count"`
	Duration     float64          `json:"duration"`
	Chapters     []YouTubeChapter `json:"chapters"`
	ThumbnailURL string           `json:"thumbnail"`
}

// YouTubeChapter represents a chapter within a YouTube video.
type YouTubeChapter struct {
	Title     string  `json:"title"`
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
}

// ListChannelVideosRequest configures a structured channel listing via
// yt-dlp --flat-playlist --dump-json. PR 4 (June 2026).
type ListChannelVideosRequest struct {
	ChannelURL  string
	DateAfter   string // YYYYMMDD format, optional
	PlaylistEnd int    // 0 = all videos, >0 = limit
}

// ListChannelVideos lists videos from a channel using structured JSON output.
// PR 4 (June 2026): replaces the text-based ListChannel with structured output.
func (d *YTDLPDownloader) ListChannelVideos(ctx context.Context, req ListChannelVideosRequest) ([]VideoInfo, error) {
	if err := security.ValidateDownloadURL(req.ChannelURL); err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	args := append([]string{}, d.cmdBuilder.BaseArgs(req.ChannelURL, d.cookiesPath != "")...)
	args = append(args, "--flat-playlist", "--dump-json")

	if req.PlaylistEnd > 0 {
		args = append(args, "--playlist-end", fmt.Sprintf("%d", req.PlaylistEnd))
	}
	if req.DateAfter != "" {
		args = append(args, "--dateafter", req.DateAfter)
	}

	args = append(args, req.ChannelURL)

	result, err := d.run(ctx, args, process.Options{
		Timeout: 60 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("yt-dlp failed: %w", err)
	}

	output := result.Stdout
	if output == "" {
		output = result.Output
	}
	var videos []VideoInfo
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var info VideoInfo
		if err := json.Unmarshal([]byte(line), &info); err != nil {
			return nil, fmt.Errorf("failed to parse yt-dlp channel listing line %q: %w", line, err)
		}
		videos = append(videos, info)
	}

	return videos, nil
}

// GetVideoMetadata fetches full metadata for a YouTube video using yt-dlp --dump-json.
func (d *YTDLPDownloader) GetVideoMetadata(ctx context.Context, videoURL string) (*YouTubeMetadata, error) {
	if err := security.ValidateDownloadURL(videoURL); err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	args := []string{
		"--no-playlist",
		"--dump-json",
	}

	// Blocco 5 (July 2026): use the centralized cmdBuilder for YouTube-
	// specific args (cookies, JS runtime, --no-warnings, --extractor-args).
	// Metadata calls use addFormat=false (format selection would conflict
	// with --dump-json).
	// The shared CommandBuilder owns the canonical cookie path. Do not
	// stat or read the cookie file here: yt-dlp must receive the same
	// configured path on every metadata attempt, including retries.
	args = append(args, d.cmdBuilder.BaseArgs(videoURL, d.cookiesPath != "")...)

	args = append(args, videoURL)

	result, err := d.run(ctx, args, process.Options{
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("yt-dlp metadata failed: %w", err)
	}

	// Use Stdout (not Output) since CombinedOutput is false
	output := result.Stdout
	if output == "" {
		output = result.Output
	}
	var meta YouTubeMetadata
	if err := json.Unmarshal([]byte(output), &meta); err != nil {
		return nil, fmt.Errorf("failed to parse yt-dlp metadata: %w (output length: %d)", err, len(output))
	}

	if meta.ID == "" {
		meta.ID = extractIDFromURL(videoURL)
	}

	return &meta, nil
}

// extractIDFromURL extracts the video ID from a YouTube URL as fallback.
func extractIDFromURL(url string) string {
	// Simple extraction for standard youtube.com/watch?v=ID and youtu.be/ID formats
	if strings.Contains(url, "youtube.com/watch") {
		for _, part := range strings.Split(url, "&") {
			if strings.HasPrefix(part, "v=") || strings.Contains(part, "?v=") {
				if idx := strings.Index(part, "v="); idx != -1 {
					id := part[idx+2:]
					if len(id) > 11 {
						id = id[:11]
					}
					return id
				}
			}
		}
	}
	if idx := strings.LastIndex(url, "/"); idx != -1 {
		return url[idx+1:]
	}
	return url
}

// ListChannel lists videos from a channel URL or ytsearch query.
func (d *YTDLPDownloader) ListChannel(ctx context.Context, channelURL string, limit int) ([]VideoInfo, error) {
	// ytsearch queries are internal yt-dlp features, not real URLs
	if !strings.HasPrefix(channelURL, "ytsearch") {
		if err := security.ValidateDownloadURL(channelURL); err != nil {
			return nil, fmt.Errorf("invalid URL: %w", err)
		}
	}

	baseURL := channelURL
	if strings.HasPrefix(channelURL, "ytsearch") {
		baseURL = "https://www.youtube.com/"
	}
	args := append([]string{}, d.cmdBuilder.BaseArgs(baseURL, d.cookiesPath != "")...)
	args = append(args,
		"--flat-playlist",
		"--dump-json",
		"--playlist-end", fmt.Sprintf("%d", limit),
		channelURL,
	)

	result, err := d.run(ctx, args, process.Options{
		Timeout: 60 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("yt-dlp failed: %w", err)
	}

	// Use Stdout (not Output) since CombinedOutput is false
	output := result.Stdout
	if output == "" {
		output = result.Output
	}
	var videos []VideoInfo
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var info VideoInfo
		if err := json.Unmarshal([]byte(line), &info); err != nil {
			return nil, fmt.Errorf("failed to parse yt-dlp channel listing line %q: %w", line, err)
		}
		videos = append(videos, info)
	}

	return videos, nil
}

// Check checks if yt-dlp is available.
func (d *YTDLPDownloader) Check() bool {
	return process.CommandExists(d.path)
}

// Version returns the yt-dlp version.
func (d *YTDLPDownloader) Version(ctx context.Context) (string, error) {
	result, err := process.Run(ctx, d.path, []string{"--version"}, process.Options{
		Timeout: 10 * time.Second,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}
