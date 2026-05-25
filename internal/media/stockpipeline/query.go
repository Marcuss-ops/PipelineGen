package stockpipeline

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"velox/go-master/internal/sources/youtube"
)

// resolveQuery converts a query string into a list of VideoSource entries.
// If the query is a YouTube URL, it returns it directly. Otherwise it searches
// YouTube using yt-dlp. The result count is read from cfg.Video.SearchCount.
func (s *Service) resolveQuery(ctx context.Context, query string) ([]VideoSource, error) {
	query = strings.TrimSpace(query)

	if strings.HasPrefix(query, "http") && (strings.Contains(query, "youtube.com") || strings.Contains(query, "youtu.be")) {
		return []VideoSource{{
			URL:    query,
			Title:  extractVideoID(query),
			Source: query,
		}}, nil
	}

	vCfg := s.cfg.Video.WithDefaults()
	numVideos := vCfg.SearchCount
	searchTerm := query

	if idx := strings.LastIndex(query, " -"); idx > 0 {
		searchTerm = strings.TrimSpace(query[:idx])
		countStr := strings.TrimSpace(query[idx+2:])
		if c, err := fmt.Sscanf(countStr, "%d", &numVideos); err != nil || c == 0 {
			numVideos = vCfg.SearchCount
		}
	}
	if numVideos < 1 {
		numVideos = 1
	}
	if numVideos > 50 {
		numVideos = 50
	}

	s.log.Info("searching YouTube", zap.String("term", searchTerm), zap.Int("count", numVideos))

	searchURL := fmt.Sprintf("ytsearch%d:%s", numVideos, searchTerm)
	videos, err := s.ytdlp.ListChannel(ctx, searchURL, numVideos)
	if err != nil {
		videos, err = s.ytdlp.ListChannel(ctx, query, numVideos)
		if err != nil {
			return nil, fmt.Errorf("failed to list videos for query %q: %w", query, err)
		}
	}

	var sources []VideoSource
	for _, v := range videos {
		url := fmt.Sprintf("https://www.youtube.com/watch?v=%s", v.ID)
		title := v.Title
		if title == "" {
			title = v.ID
		}
		sources = append(sources, VideoSource{
			URL:         url,
			Title:       title,
			Source:      url,
			DurationSec: v.Duration,
		})
	}

	return sources, nil
}

// getDirectVideoInfo fetches metadata for a direct YouTube URL.
func (s *Service) getDirectVideoInfo(ctx context.Context, videoURL string) (*youtube.VideoMetadata, error) {
	if s.youtubeSvc == nil {
		return nil, nil
	}
	return s.youtubeSvc.GetVideoInfo(ctx, videoURL)
}
