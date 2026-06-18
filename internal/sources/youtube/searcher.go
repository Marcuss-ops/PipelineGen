package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"

	"go.uber.org/zap"
)

type searchL1Entry struct {
	Results []asset.MediaAsset
	AddedAt time.Time
}

type metadataL1Entry struct {
	Metadata *VideoMetadata
	AddedAt  time.Time
}

// SearchLive performs a live YouTube search using yt-dlp.
// sort can be "views" for most viewed videos.
func (s *Service) SearchLive(ctx context.Context, query string, limit int, sort string) ([]asset.MediaAsset, error) {
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

	cacheKey := fmt.Sprintf("%s|%d|%s", query, limit, sort)

	// 1. Check L1 memory cache
	if val, ok := s.searchL1.Load(cacheKey); ok {
		if entry, ok := val.(searchL1Entry); ok {
			if time.Since(entry.AddedAt) < 6*time.Hour {
				s.log.Info("Serving YouTube search results from L1 cache", zap.String("query", query))
				return entry.Results, nil
			}
		}
	}

	// 2. Check L2 SQLite cache
	if cached, ok := s.getCachedSearch(ctx, cacheKey); ok {
		s.log.Info("Serving YouTube search results from L2 SQLite cache", zap.String("query", query))
		// Populate L1 cache
		s.searchL1.Store(cacheKey, searchL1Entry{
			Results: cached,
			AddedAt: time.Now(),
		})
		return cached, nil
	}

	s.log.Info("Performing live YouTube search", zap.String("query", query), zap.Int("limit", limit), zap.String("sort", sort))

	ytdlpPath := s.cfg.External.ResolvedYtdlpPath()

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

	// Add cookies if config or local cookies.txt exists
	cookiesPath := s.cfg.External.YouTubeCookiesPath
	if cookiesPath == "" {
		cookiesPath = "cookies.txt"
	}
	if _, err := os.Stat(cookiesPath); err == nil {
		args = append(args, "--cookies", cookiesPath)
		s.log.Debug("Adding cookies file to yt-dlp search", zap.String("path", cookiesPath))
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
	results := make([]asset.MediaAsset, 0, len(lines))

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

		metadata := map[string]any{
			"uploader":  item.Uploader,
			"duration":  item.Duration,
			"video_id":  item.ID,
		}

		results = append(results, asset.MediaAsset{
			ID:           "youtube_" + item.ID,
			Name:         item.Title,
			Source:       "youtube",
			SourceURL:    item.URL,
			ExternalURL:  item.URL,
			ThumbnailURL: thumbnail,
			Metadata:     metadata,
		})
	}

	// Cache the search results in L1 and L2
	s.setCachedSearch(ctx, cacheKey, results)
	s.searchL1.Store(cacheKey, searchL1Entry{
		Results: results,
		AddedAt: time.Now(),
	})

	return results, nil
}
