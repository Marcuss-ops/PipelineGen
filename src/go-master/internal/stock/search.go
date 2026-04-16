// Package stock provides search functionality for stock videos.
package stock

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	youtube "velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// Search queries the requested sources and normalizes results.
func (m *StockManager) Search(ctx context.Context, query string, sources []string) ([]SearchResult, error) {
	if len(sources) == 0 {
		sources = []string{"youtube"}
	}

	var results []SearchResult
	for _, source := range sources {
		switch source {
		case "youtube":
			videos, err := m.SearchYouTube(ctx, query, 10)
			if err != nil {
				logger.Warn("YouTube search failed", zap.Error(err))
				continue
			}
			for _, v := range videos {
				results = append(results, SearchResult{
					Source:      "youtube",
					Title:       v.Title,
					URL:         v.URL,
					Duration:    v.Duration,
					Thumbnail:   v.Thumbnail,
					Description: v.Description,
				})
			}
		}
	}

	return results, nil
}

// findYtDlp finds the yt-dlp executable path
func findYtDlp() string {
	// Try common paths
	paths := []string{
		"yt-dlp",
		"/usr/local/bin/yt-dlp",
		"/usr/bin/yt-dlp",
	}

	for _, path := range paths {
		cmd := exec.Command(path, "--version")
		if err := cmd.Run(); err == nil {
			return path
		}
	}

	return ""
}

// SearchYouTube searches YouTube through the YouTube v2 client (more reliable).
func (m *StockManager) SearchYouTube(ctx context.Context, query string, maxResults int) ([]VideoResult, error) {
	if maxResults <= 0 {
		maxResults = 10
	}
	if maxResults > 50 {
		maxResults = 50
	}

	logger.Info("SearchYouTube called",
		zap.String("query", query),
		zap.Int("maxResults", maxResults),
		zap.Bool("hasYtClient", m.ytClient != nil),
	)

	// Use YouTube v2 client if available
	if m.ytClient != nil {
		logger.Info("SearchYouTube using YouTube v2 client",
			zap.String("query", query),
			zap.Int("maxResults", maxResults),
		)

		results, err := m.ytClient.Search(ctx, query, &youtube.SearchOptions{MaxResults: maxResults})
		if err != nil {
			logger.Warn("YouTube v2 client search failed, falling back to yt-dlp",
				zap.String("query", query),
				zap.Error(err),
			)
			// Fall through to yt-dlp fallback
		} else {
			// Convert YouTube v2 search results to VideoResult
			var videoResults []VideoResult
			for _, r := range results {
				videoResults = append(videoResults, VideoResult{
					ID:          r.ID,
					Source:      "youtube",
					Title:       r.Title,
					Description: r.Description,
					Duration:    int(r.Duration.Seconds()),
					Thumbnail:   r.Thumbnail,
					URL:         r.URL,
					Uploader:    r.Channel,
					ViewCount:   int(r.Views),
				})
			}

			logger.Info("YouTube v2 search completed",
				zap.String("query", query),
				zap.Int("results", len(videoResults)),
			)
			return videoResults, nil
		}
	}

	// Fallback to yt-dlp (original implementation)
	logger.Info("SearchYouTube falling back to yt-dlp",
		zap.String("query", query),
	)

	return m.searchYouTubeWithYtDlp(ctx, query, maxResults)
}

// searchYouTubeWithYtDlp is the fallback implementation using yt-dlp directly.
func (m *StockManager) searchYouTubeWithYtDlp(ctx context.Context, query string, maxResults int) ([]VideoResult, error) {
	// Find yt-dlp path
	ytDlpPath := findYtDlp()
	if ytDlpPath == "" {
		return nil, fmt.Errorf("yt-dlp not found in PATH")
	}

	args := []string{
		fmt.Sprintf("ytsearch%d:%s", maxResults, query),
		"--dump-json",
		"--flat-playlist",
		"--no-warnings",
	}

	cmd := exec.CommandContext(ctx, ytDlpPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Warn("yt-dlp fallback failed",
			zap.Error(err),
			zap.Int("outputBytes", len(output)),
		)
		return nil, fmt.Errorf("yt-dlp search failed: %w", err)
	}

	var results []VideoResult
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}

		var video struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Duration    int    `json:"duration"`
			Thumbnail   string `json:"thumbnail"`
			Uploader    string `json:"uploader"`
			ViewCount   int    `json:"view_count"`
			URL         string `json:"url"`
			Thumbnails  []struct {
				URL    string `json:"url"`
				Height int    `json:"height"`
				Width  int    `json:"width"`
			} `json:"thumbnails"`
		}
		if err := json.Unmarshal([]byte(line), &video); err != nil {
			continue
		}

		if video.ID == "" {
			continue
		}

		thumbnail := video.Thumbnail
		if thumbnail == "" && len(video.Thumbnails) > 0 {
			thumbnail = video.Thumbnails[0].URL
		}

		url := video.URL
		if url == "" {
			url = fmt.Sprintf("https://youtube.com/watch?v=%s", video.ID)
		}

		results = append(results, VideoResult{
			ID:          video.ID,
			Source:      "youtube",
			Title:       video.Title,
			Description: video.Description,
			Duration:    video.Duration,
			Thumbnail:   thumbnail,
			URL:         url,
			Uploader:    video.Uploader,
			ViewCount:   video.ViewCount,
		})
	}

	logger.Info("YouTube yt-dlp fallback completed", zap.String("query", query), zap.Int("results", len(results)))
	return results, nil
}
