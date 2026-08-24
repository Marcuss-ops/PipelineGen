package assets

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// resolveQuery converts a query string into a list of VideoSource entries.
// If the query is a YouTube URL, it returns it directly. Otherwise it searches
// YouTube. `limit` is the caller-supplied per-query result cap: a positive
// value wins over the runtime default (RuntimeConfig.MaxResults), which is
// still used when limit <= 0. The legacy " -N" suffix convention is honoured
// only when the caller did not supply an explicit limit.
func (s *Service) resolveQuery(ctx context.Context, query string, limit int) ([]VideoSource, error) {
	query = strings.TrimSpace(query)

	if strings.HasPrefix(query, "http") && (strings.Contains(query, "youtube.com") || strings.Contains(query, "youtu.be")) {
		videoID, _ := urlutil.ExtractVideoID(query)
		title := videoID
		if info, err := s.getDirectVideoInfo(ctx, query); err == nil && info != nil {
			if info.Title != "" {
				title = info.Title
			}
			return []VideoSource{{
				URL:         query,
				Title:       title,
				Source:      query,
				DurationSec: info.Duration,
			}}, nil
		}
		return []VideoSource{{
			URL:    query,
			Title:  title,
			Source: query,
		}}, nil
	}

	numVideos := limit
	if numVideos < 1 {
		numVideos = s.runtime.MaxResults
	}
	searchTerm := query

	if limit < 1 {
		if idx := strings.LastIndex(query, " -"); idx > 0 {
			searchTerm = strings.TrimSpace(query[:idx])
			countStr := strings.TrimSpace(query[idx+2:])
			if c, err := fmt.Sscanf(countStr, "%d", &numVideos); err != nil || c == 0 {
				numVideos = s.runtime.MaxResults
			}
		}
	}
	if numVideos < 1 {
		numVideos = 1
	}
	if numVideos > 50 {
		numVideos = 50
	}

	s.log.Info("searching YouTube", zap.String("term", searchTerm), zap.Int("count", numVideos))

	if s.channelLister == nil {
		return nil, fmt.Errorf("resolveQuery: channelLister port is nil — must be wired at composition root (P4, July 2026)")
	}
	searchURL := fmt.Sprintf("ytsearch%d:%s", numVideos, searchTerm)
	videos, err := s.channelLister.ListChannel(ctx, searchURL, numVideos)
	if err != nil {
		videos, err = s.channelLister.ListChannel(ctx, query, numVideos)
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

// Search resolves a text query into a list of VideoSource entries via
// the YouTube channel lister, honouring an explicit caller-supplied
// result limit. It is the canonical public search surface for the stock
// provider (the SearchProvider adapter delegates here), kept separate
// from resolveQuery (which applies pipeline run defaults and URL
// passthrough). A nil channelLister fails closed with a typed error.
func (s *Service) Search(ctx context.Context, query string, limit int) ([]VideoSource, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("stock search: query is required")
	}
	numVideos := limit
	if numVideos < 1 {
		numVideos = 1
	}
	if numVideos > 50 {
		numVideos = 50
	}
	if s.channelLister == nil {
		return nil, fmt.Errorf("stock search: channelLister port is nil — must be wired at composition root (P4, July 2026)")
	}
	searchURL := fmt.Sprintf("ytsearch%d:%s", numVideos, query)
	videos, err := s.channelLister.ListChannel(ctx, searchURL, numVideos)
	if err != nil {
		videos, err = s.channelLister.ListChannel(ctx, query, numVideos)
		if err != nil {
			return nil, fmt.Errorf("stock search: failed to list videos for query %q: %w", query, err)
		}
	}
	sources := make([]VideoSource, 0, len(videos))
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
// P8 (July 2026): youtubeSvc field REMOVED from Service — dead code
// (never wired at composition root). Always returns nil, nil.
func (s *Service) getDirectVideoInfo(_ context.Context, _ string) (*youtubeports.DownloaderMetadata, error) {
	return nil, nil
}
