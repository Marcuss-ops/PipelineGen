package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/security"
)

// SearchLive performs a live YouTube search using yt-dlp.
// sort can be "views" for most viewed videos.
func (s *Service) SearchLive(ctx context.Context, query string, limit int, sort string) ([]models.MediaAsset, error) {
	// Parse limit from query if present (e.g., "query -15")
	if strings.Contains(query, " -") {
		parts := strings.Split(query, " -")
		if len(parts) > 1 {
			if l, err := strconv.Atoi(parts[len(parts)-1]); err == nil && l > 0 {
				limit = l
				query = strings.Join(parts[:len(parts)-1], " -")
			}
		}
	}

	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	s.log.Info("Performing live YouTube search", zap.String("query", query), zap.Int("limit", limit), zap.String("sort", sort))

	ytdlpPath := s.cfg.External.YtdlpPath
	if ytdlpPath == "" {
		ytdlpPath = "yt-dlp"
	}

	var searchQuery string
	var args []string

	if sort == "views" {
		// Use YouTube search URL with view count filter (sp=CAM%253D)
		searchQuery = fmt.Sprintf("https://www.youtube.com/results?search_query=%s&sp=CAM%%253D", url.QueryEscape(query))
		args = []string{
			searchQuery,
			"--dump-json",
			"--flat-playlist",
			"--no-warnings",
			"--playlist-end", strconv.Itoa(limit),
		}
	} else {
		// Use standard ytsearchN:query format
		searchQuery = fmt.Sprintf("ytsearch%d:%s", limit, query)
		args = []string{
			searchQuery,
			"--dump-json",
			"--flat-playlist",
			"--no-warnings",
		}
	}

	cmd := exec.CommandContext(ctx, ytdlpPath, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		s.log.Error("yt-dlp search failed", zap.Error(err), zap.String("stderr", stderr.String()))
		return nil, fmt.Errorf("search failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	results := make([]models.MediaAsset, 0, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}

		var item struct {
			ID         string  `json:"id"`
			URL        string  `json:"url"`
			Title      string  `json:"title"`
			Duration   float64 `json:"duration"`
			Uploader   string  `json:"uploader"`
			Thumbnails []struct {
				URL string `json:"url"`
			} `json:"thumbnails"`
		}

		if err := json.Unmarshal([]byte(line), &item); err != nil {
			s.log.Warn("failed to unmarshal search result line", zap.Error(err))
			continue
		}

		thumbnail := ""
		if len(item.Thumbnails) > 0 {
			thumbnail = item.Thumbnails[len(item.Thumbnails)-1].URL
		}

		clip := models.MediaAsset{
			ID:           "youtube_" + item.ID,
			Name:         item.Title,
			Source:       "youtube",
			ExternalURL:  item.URL,
			DownloadLink: item.URL,
			ThumbURL:     thumbnail,
		}
		clip.SetMetadataJSON(fmt.Sprintf(`{"uploader": %q, "duration": %f, "video_id": %q}`, item.Uploader, item.Duration, item.ID))
		results = append(results, clip)
	}

	return results, nil
}

// GetVideoInfo retrieves full metadata for a YouTube video without downloading it
func (s *Service) GetVideoInfo(ctx context.Context, videoURL string) (*VideoMetadata, error) {
	if videoURL == "" {
		return nil, fmt.Errorf("url is required")
	}

	if err := security.ValidateDownloadURL(videoURL); err != nil {
		return nil, err
	}

	s.log.Info("Retrieving YouTube video info", zap.String("url", videoURL))

	ytdlpPath := s.cfg.External.YtdlpPath
	if ytdlpPath == "" {
		ytdlpPath = "yt-dlp"
	}

	args := []string{
		videoURL,
		"--dump-json",
		"--no-playlist",
		"--no-warnings",
	}

	cmd := exec.CommandContext(ctx, ytdlpPath, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		s.log.Error("yt-dlp info failed", zap.Error(err), zap.String("stderr", stderr.String()))
		return nil, fmt.Errorf("failed to get video info: %w", err)
	}

	var raw struct {
		ID          string  `json:"id"`
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Duration    float64 `json:"duration"`
		Uploader    string  `json:"uploader"`
		UploadDate  string  `json:"upload_date"`
		ViewCount   int64   `json:"view_count"`
		Thumbnails  []struct {
			URL    string `json:"url"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		} `json:"thumbnails"`
		Chapters []struct {
			Title     string  `json:"title"`
			StartTime float64 `json:"start_time"`
			EndTime   float64 `json:"end_time"`
		} `json:"chapters"`
		Categories []string `json:"categories"`
		Tags       []string `json:"tags"`
	}

	if err := json.Unmarshal([]byte(stdout.String()), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse video info: %w", err)
	}

	metadata := &VideoMetadata{
		ID:          raw.ID,
		URL:         videoURL,
		Title:       raw.Title,
		Description: raw.Description,
		Duration:    raw.Duration,
		Uploader:    raw.Uploader,
		UploadDate:  raw.UploadDate,
		ViewCount:   raw.ViewCount,
		Categories:  raw.Categories,
		Tags:        raw.Tags,
	}

	// Process thumbnails
	if len(raw.Thumbnails) > 0 {
		metadata.ThumbnailURL = raw.Thumbnails[len(raw.Thumbnails)-1].URL
		for _, t := range raw.Thumbnails {
			metadata.Thumbnails = append(metadata.Thumbnails, VideoThumbnail{
				URL:    t.URL,
				Width:  t.Width,
				Height: t.Height,
			})
		}
	}

	// Process chapters
	for _, c := range raw.Chapters {
		metadata.Chapters = append(metadata.Chapters, VideoChapter{
			Title:     c.Title,
			StartTime: c.StartTime,
			EndTime:   c.EndTime,
		})
	}

	return metadata, nil
}
