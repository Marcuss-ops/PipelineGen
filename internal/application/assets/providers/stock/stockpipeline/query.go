package stockpipeline

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// resolveQuery converts a query string into a list of VideoSource entries.
// If the query is a YouTube URL, it returns it directly. Otherwise it searches
// YouTube using the minimal runtime result limit.
func (s *Service) resolveQuery(ctx context.Context, query string) ([]VideoSource, error) {
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

	numVideos := s.runtime.MaxResults
	searchTerm := query

	if idx := strings.LastIndex(query, " -"); idx > 0 {
		searchTerm = strings.TrimSpace(query[:idx])
		countStr := strings.TrimSpace(query[idx+2:])
		if c, err := fmt.Sscanf(countStr, "%d", &numVideos); err != nil || c == 0 {
			numVideos = s.runtime.MaxResults
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

// getDirectVideoInfo fetches metadata for a direct YouTube URL.
// P8 (July 2026): youtubeSvc field REMOVED from Service — dead code
// (never wired at composition root). Always returns nil, nil.
func (s *Service) getDirectVideoInfo(_ context.Context, _ string) (*youtubeports.DownloaderMetadata, error) {
	return nil, nil
}

// expandSearchQueries parses flags like --interview 5 and splits/adjusts query counts.
// E.g., "Floyd Mayweather -15 --interview 5" -> ["Floyd Mayweather -10", "Floyd Mayweather interview -5"]
func expandSearchQueries(queries []string, log *zap.Logger) []string {
	var result []string
	for _, q := range queries {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}

		words := strings.Fields(q)
		var cleanWords []string
		type subQuery struct {
			word  string
			count int
		}
		var subs []subQuery

		for i := 0; i < len(words); i++ {
			w := words[i]
			if strings.HasPrefix(w, "--") && len(w) > 2 {
				if i+1 < len(words) {
					var count int
					if _, err := fmt.Sscanf(words[i+1], "%d", &count); err == nil && count > 0 {
						flagName := strings.TrimPrefix(w, "--")
						subs = append(subs, subQuery{word: flagName, count: count})
						i++ // skip count
						continue
					}
				}
			}
			cleanWords = append(cleanWords, w)
		}

		mainQ := strings.Join(cleanWords, " ")
		if len(subs) == 0 {
			result = append(result, mainQ)
			continue
		}

		mainTerm := mainQ
		mainCount := 15 // default
		if idx := strings.LastIndex(mainQ, " -"); idx > 0 {
			mainTerm = strings.TrimSpace(mainQ[:idx])
			countStr := strings.TrimSpace(mainQ[idx+2:])
			var c int
			if _, err := fmt.Sscanf(countStr, "%d", &c); err == nil && c > 0 {
				mainCount = c
			}
		}

		totalSubCount := 0
		for _, sub := range subs {
			totalSubCount += sub.count
		}

		if mainCount > totalSubCount {
			mainCount -= totalSubCount
		} else {
			mainCount = 1
		}

		result = append(result, fmt.Sprintf("%s -%d", mainTerm, mainCount))
		log.Info("Adjusted main stock query", zap.String("term", mainTerm), zap.Int("new_count", mainCount))

		for _, sub := range subs {
			subQ := fmt.Sprintf("%s %s -%d", mainTerm, sub.word, sub.count)
			result = append(result, subQ)
			log.Info("Added sub-query for stock pipeline", zap.String("query", subQ))
		}
	}
	return result
}
