// Package stock provides search functionality for stock videos.
package stock

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	youtube "velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"
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

type SearchYouTubeOptions struct {
	MaxResults int
	SortBy     string
	UploadDate string
	Duration   string
}

// SearchYouTubeWithOptions searches YouTube with explicit sort/date filters and returns ranked results.
func (m *StockManager) SearchYouTubeWithOptions(ctx context.Context, query string, opts SearchYouTubeOptions) ([]VideoResult, error) {
	if opts.MaxResults <= 0 {
		opts.MaxResults = 10
	}
	if opts.MaxResults > 50 {
		opts.MaxResults = 50
	}
	if strings.TrimSpace(opts.SortBy) == "" {
		opts.SortBy = "views"
	}
	if strings.TrimSpace(opts.UploadDate) == "" {
		opts.UploadDate = "week"
	}

	if m.ytClient != nil {
		results, err := m.ytClient.Search(ctx, query, &youtube.SearchOptions{
			MaxResults: opts.MaxResults,
			SortBy:     opts.SortBy,
			UploadDate: opts.UploadDate,
			Duration:   opts.Duration,
		})
		if err == nil {
			videoResults := make([]VideoResult, 0, len(results))
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
			videoResults = filterByUploadDate(videoResults, opts.UploadDate)
			sort.Slice(videoResults, func(i, j int) bool { return videoResults[i].ViewCount > videoResults[j].ViewCount })
			return videoResults[:minInt(len(videoResults), opts.MaxResults)], nil
		}
		logger.Warn("SearchYouTubeWithOptions: yt client failed, fallback to yt-dlp",
			zap.String("query", query),
			zap.Error(err),
		)
	}

	return m.searchYouTubeWithYtDlpOptions(ctx, query, opts)
}

// searchYouTubeWithYtDlp is the fallback implementation using yt-dlp directly.
func (m *StockManager) searchYouTubeWithYtDlp(ctx context.Context, query string, maxResults int) ([]VideoResult, error) {
	// Find yt-dlp path
	ytDlpPath := findYtDlp()
	if ytDlpPath == "" {
		return nil, fmt.Errorf("yt-dlp not found in PATH")
	}

	// Sanitize query to prevent injection (yt-dlp takes it as a single arg)
	if strings.ContainsAny(query, "`;|&$\"'\\") {
		return nil, fmt.Errorf("search query contains forbidden characters")
	}
	if len(query) > 200 {
		return nil, fmt.Errorf("search query too long (max 200 chars)")
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

func (m *StockManager) searchYouTubeWithYtDlpOptions(ctx context.Context, query string, opts SearchYouTubeOptions) ([]VideoResult, error) {
	ytDlpPath := findYtDlp()
	if ytDlpPath == "" {
		return nil, fmt.Errorf("yt-dlp not found in PATH")
	}

	if strings.ContainsAny(query, "`;|&$\"'\\") {
		return nil, fmt.Errorf("search query contains forbidden characters")
	}
	if len(query) > 200 {
		return nil, fmt.Errorf("search query too long (max 200 chars)")
	}

	args := []string{
		fmt.Sprintf("ytsearch%d:%s", opts.MaxResults, query),
		"--dump-json",
		"--flat-playlist",
		"--no-warnings",
		"--extractor-args", "youtube:skip=dash,hls",
		"--match-filter", mapDurationForStock(opts.Duration),
	}
	if dateAfter := mapUploadDateForStock(opts.UploadDate); dateAfter != "" {
		args = append(args, "--dateafter", dateAfter)
	}
	args = append(args, "--playlist-end", strconv.Itoa(opts.MaxResults))

	cmd := exec.CommandContext(ctx, ytDlpPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
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
			UploadDate  string `json:"upload_date"`
		}
		if err := json.Unmarshal([]byte(line), &video); err != nil {
			continue
		}
		if video.ID == "" {
			continue
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
			Thumbnail:   video.Thumbnail,
			URL:         url,
			Uploader:    video.Uploader,
			ViewCount:   video.ViewCount,
			UploadDate:  normalizeUploadDate(video.UploadDate),
		})
	}
	results = filterByUploadDate(results, opts.UploadDate)
	sort.Slice(results, func(i, j int) bool { return results[i].ViewCount > results[j].ViewCount })
	return results[:minInt(len(results), opts.MaxResults)], nil
}

func mapUploadDateForStock(uploadDate string) string {
	now := time.Now().UTC()
	switch strings.ToLower(strings.TrimSpace(uploadDate)) {
	case "hour":
		return now.Add(-1 * time.Hour).Format("20060102")
	case "today":
		return now.Add(-24 * time.Hour).Format("20060102")
	case "week":
		return now.Add(-7 * 24 * time.Hour).Format("20060102")
	case "month":
		return now.Add(-30 * 24 * time.Hour).Format("20060102")
	case "year":
		return now.Add(-365 * 24 * time.Hour).Format("20060102")
	default:
		return ""
	}
}

func mapDurationForStock(duration string) string {
	switch strings.ToLower(strings.TrimSpace(duration)) {
	case "short":
		return "duration < 240"
	case "medium":
		return "duration >= 240 & duration <= 1200"
	case "long":
		return "duration > 1200"
	default:
		return "duration > 0"
	}
}

func normalizeUploadDate(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) == 8 {
		return fmt.Sprintf("%s-%s-%s", raw[:4], raw[4:6], raw[6:8])
	}
	return raw
}

func filterByUploadDate(results []VideoResult, uploadDate string) []VideoResult {
	uploadDate = strings.ToLower(strings.TrimSpace(uploadDate))
	if uploadDate == "" {
		return results
	}
	var days int
	switch uploadDate {
	case "hour":
		days = 1
	case "today":
		days = 1
	case "week":
		days = 7
	case "month":
		days = 30
	case "year":
		days = 365
	default:
		return results
	}
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	filtered := make([]VideoResult, 0, len(results))
	for _, r := range results {
		d := strings.TrimSpace(r.UploadDate)
		if d == "" {
			continue
		}
		if t, err := time.Parse("2006-01-02", d); err == nil {
			if t.After(cutoff) || t.Equal(cutoff) {
				filtered = append(filtered, r)
			}
		}
	}
	if len(filtered) == 0 {
		return results
	}
	return filtered
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
