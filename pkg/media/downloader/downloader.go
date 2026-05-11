// Package downloader provides YouTube/social media download capabilities via yt-dlp.
//
// STATUS: ACTIVE - This package is actively used by mediaasset.Processor, mediapipeline, and artlist service.
package downloader

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/pkg/config"
	"velox/go-master/pkg/executil"
	"velox/go-master/pkg/security"
)

// YTDLPDownloader handles YouTube/social media downloads via yt-dlp.
type YTDLPDownloader struct {
	path string
}

// NewYTDLP creates a new yt-dlp downloader.
func NewYTDLP(cfg *config.Config) *YTDLPDownloader {
	path := cfg.External.YtdlpPath
	if path == "" {
		path = "yt-dlp"
	}
	return &YTDLPDownloader{path: path}
}

// DownloadRequest configures a download operation.
type DownloadRequest struct {
	URL            string
	OutputPath     string
	Format         string // e.g. "bv*[ext=mp4]+ba[ext=m4a]/b[ext=mp4]/best"
	MergeFormat    string // e.g. "mp4"
	NoPlaylist     bool
	DownloadSections []string // e.g. ["*00:01:20-00:01:35"]
	ForceKeyframes bool
	StreamCopy     bool // If true, force stream copy (fast but less precise)
	Timeout        time.Duration
}

// DownloadedSegment represents a successfully downloaded segment.
type DownloadedSegment struct {
	Path   string
	Name   string
	Index  int
}

// Download downloads a full video.
func (d *YTDLPDownloader) Download(ctx context.Context, req *DownloadRequest) error {
	if err := security.ValidateDownloadURL(req.URL); err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	args := []string{"--no-playlist"}

	// Add Artlist-specific args (cookies, headers, impersonation)
	if strings.Contains(req.URL, "artlist") {
		args = append(args, "--cookies", "/tmp/artlist_cookies.txt")
		args = append(args, "--add-header", "Referer:https://artlist.io/")
		args = append(args, "--add-header", "Origin:https://artlist.io/")
		args = append(args, "--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
		args = append(args, "--extractor-args", "generic:impersonate")
	}

	if req.Format != "" {
		args = append(args, "-f", req.Format)
	}
	if req.MergeFormat != "" {
		args = append(args, "--merge-output-format", req.MergeFormat)
	}

	outputTemplate := req.OutputPath
	if !strings.Contains(outputTemplate, "%(ext)s") {
		outputTemplate = outputTemplate + ".%(ext)s"
	}
	args = append(args, "-o", outputTemplate)

	if len(req.DownloadSections) > 0 {
		for _, section := range req.DownloadSections {
			args = append(args, "--download-sections", section)
		}
		if req.ForceKeyframes {
			args = append(args, "--force-keyframes-at-cuts")
		} else if req.StreamCopy {
			args = append(args, "--downloader-args", "ffmpeg:-c copy")
		}
	}

	args = append(args, req.URL)

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}

	_, err := executil.Run(ctx, d.path, args, executil.Options{
		Timeout:        timeout,
		CombinedOutput: true,
	})
	return err
}

// DownloadSections downloads specific time sections from a video.
// Returns paths to downloaded segment files.
func (d *YTDLPDownloader) DownloadSections(ctx context.Context, req *DownloadRequest) ([]DownloadedSegment, error) {
	if err := security.ValidateDownloadURL(req.URL); err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	if len(req.DownloadSections) == 0 {
		return nil, fmt.Errorf("no download sections specified")
	}

	// Create output directory if needed
	outputDir := filepath.Dir(req.OutputPath)
	if outputDir != "." && outputDir != "" {
		if _, err := executil.RunSimple(ctx, "mkdir", "-p", outputDir); err != nil {
			return nil, fmt.Errorf("failed to create output dir: %w", err)
		}
	}

	var results []DownloadedSegment
	for i, section := range req.DownloadSections {
		// Validate timestamp format
		if err := security.SanitizeTimestamp(section); err != nil {
			return nil, fmt.Errorf("invalid section %d: %w", i, err)
		}

		// Use a safe name for the template based on index and a slug of the URL or a constant
		outputTemplate := filepath.Join(outputDir, fmt.Sprintf("%03d_segment.%%(ext)s", i+1))
		args := []string{"--no-playlist"}

		if req.Format != "" {
			args = append(args, "-f", req.Format)
		}
		if req.MergeFormat != "" {
			args = append(args, "--merge-output-format", req.MergeFormat)
		}

		args = append(args, "--download-sections", section)
		if req.ForceKeyframes {
			args = append(args, "--force-keyframes-at-cuts")
		}
		args = append(args, "-o", outputTemplate)
		args = append(args, req.URL)

		_, err := executil.Run(ctx, d.path, args, executil.Options{
			Timeout:        10 * time.Minute,
			CombinedOutput: true,
		})
		if err != nil {
			return results, fmt.Errorf("failed to download section %d: %w", i, err)
		}

		results = append(results, DownloadedSegment{
			Path:  outputTemplate,
			Name:  fmt.Sprintf("segment_%03d", i+1),
			Index: i,
		})
	}

	return results, nil
}

// ListChannelVideos lists videos from a YouTube channel using flat playlist.
type VideoInfo struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Views    int64  `json:"view_count"`
	Duration float64 `json:"duration"` // yt-dlp might return float
}

// ListChannel lists videos from a channel URL.
func (d *YTDLPDownloader) ListChannel(ctx context.Context, channelURL string, limit int) ([]VideoInfo, error) {
	if err := security.ValidateDownloadURL(channelURL); err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	args := []string{
		"--flat-playlist",
		"--dump-json",
		"--playlist-end", fmt.Sprintf("%d", limit),
		channelURL,
	}

	result, err := executil.Run(ctx, d.path, args, executil.Options{
		Timeout:        60 * time.Second,
		CombinedOutput: true,
	})
	if err != nil {
		return nil, fmt.Errorf("yt-dlp failed: %w", err)
	}

	var videos []VideoInfo
	lines := strings.Split(result.Output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var info VideoInfo
		if err := json.Unmarshal([]byte(line), &info); err == nil {
			videos = append(videos, info)
		}
	}

	return videos, nil
}

// Check checks if yt-dlp is available.
func (d *YTDLPDownloader) Check() bool {
	return executil.CommandExists(d.path)
}

// Version returns the yt-dlp version.
func (d *YTDLPDownloader) Version(ctx context.Context) (string, error) {
	result, err := executil.Run(ctx, d.path, []string{"--version"}, executil.Options{
		Timeout: 10 * time.Second,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Output), nil
}
