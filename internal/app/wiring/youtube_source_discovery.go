package adapters

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	stockplan "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockplan"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
)

// YouTubeSourceDiscoveryAdapter adapts the existing YouTube search runner to
// the VidRush discovery port. It only searches and reads metadata; it never
// downloads, uploads, writes SQLite rows, or materializes clips.
type YouTubeSourceDiscoveryAdapter struct {
	search youtubeports.SearchRunnerPort
}

func NewYouTubeSourceDiscoveryAdapter(search youtubeports.SearchRunnerPort) (*YouTubeSourceDiscoveryAdapter, error) {
	if search == nil {
		return nil, errors.New("youtube source discovery: search runner is required")
	}
	return &YouTubeSourceDiscoveryAdapter{search: search}, nil
}

// Discover searches each focused query, deduplicates by YouTube video ID and
// applies duration/live filters before returning at most MaxVideos candidates.
func (a *YouTubeSourceDiscoveryAdapter) Discover(ctx context.Context, req scriptports.VideoSourceDiscoveryRequest) ([]scriptports.VideoSourceCandidate, error) {
	if a == nil || a.search == nil {
		return nil, errors.New("youtube source discovery: search runner is unavailable")
	}
	queries := cleanDiscoveryQueries(req)
	if len(queries) == 0 {
		return nil, fmt.Errorf("%w: no queries", scriptports.ErrNoDiscoveryCandidates)
	}
	limit := req.MaxVideos
	if limit <= 0 {
		limit = 12
	}
	if limit > 50 {
		limit = 50
	}

	byID := make(map[string]scriptports.VideoSourceCandidate)
	for queryIndex, query := range queries {
		results, err := a.search.SearchLive(ctx, query, limit, "relevance")
		if err != nil {
			if len(byID) == 0 {
				return nil, fmt.Errorf("youtube source discovery: search %q: %w", query, err)
			}
			continue
		}
		for rank, result := range results {
			// SearchLive is metadata-only. Discovery must never call
			// GetVideoInfo or the video pipeline; full metadata enrichment
			// belongs to StockService.Plan after candidate selection.
			candidate, ok := discoveryCandidate(result, query, queryIndex, rank, req)
			if !ok {
				continue
			}
			if previous, exists := byID[candidate.VideoID]; !exists || candidateBetter(candidate, previous) {
				byID[candidate.VideoID] = candidate
			}
		}
	}
	if len(byID) == 0 {
		return nil, fmt.Errorf("%w for segment %q", scriptports.ErrNoDiscoveryCandidates, req.SegmentID)
	}
	out := make([]scriptports.VideoSourceCandidate, 0, len(byID))
	for _, candidate := range byID {
		out = append(out, candidate)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].MetadataScore != out[j].MetadataScore {
			return out[i].MetadataScore > out[j].MetadataScore
		}
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		return out[i].VideoID < out[j].VideoID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func cleanDiscoveryQueries(req scriptports.VideoSourceDiscoveryRequest) []string {
	out := make([]string, 0, len(req.Queries))
	seen := make(map[string]struct{}, len(req.Queries))
	for _, raw := range req.Queries {
		query := strings.TrimSpace(raw)
		key := strings.ToLower(query)
		if query == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, query)
	}
	return out
}

func discoveryCandidate(result youtubeports.SearchLiveResult, query string, queryIndex, rank int, req scriptports.VideoSourceDiscoveryRequest) (scriptports.VideoSourceCandidate, bool) {
	url := strings.TrimSpace(result.URL)
	if url == "" {
		return scriptports.VideoSourceCandidate{}, false
	}
	video, err := parseDiscoveryVideo(url, result.ID)
	if err != nil {
		return scriptports.VideoSourceCandidate{}, false
	}
	durationMs := int64(result.Duration * 1000)
	if req.MinVideoDurationMs > 0 && (durationMs == 0 || durationMs < req.MinVideoDurationMs) {
		return scriptports.VideoSourceCandidate{}, false
	}
	if req.ExcludeLive && isLiveSearchResult(result) {
		return scriptports.VideoSourceCandidate{}, false
	}
	score := 1.0 / float64(queryIndex+1)
	if rank > 0 {
		score *= 1 / (1 + float64(rank)*0.1)
	}
	return scriptports.VideoSourceCandidate{
		Provider: "youtube", VideoID: video.ID, URL: video.URL,
		Title: result.Title, DurationMs: durationMs, Query: query,
		Rank: rank, MetadataScore: clampUnit(score),
	}, true
}

func isLiveSearchResult(result youtubeports.SearchLiveResult) bool {
	// SearchLiveResult currently exposes no dedicated live-status field.
	// Treat explicit live wording as live only when duration is unknown;
	// normal uploaded videos with a title containing “live” remain valid.
	return result.Duration <= 0 && strings.Contains(strings.ToLower(result.Title), "live")
}

func parseDiscoveryVideo(rawURL, fallbackID string) (stockplan.YouTubeVideo, error) {
	video, err := stockplan.ParseYouTubeURL(rawURL)
	if err == nil {
		return video, nil
	}
	if strings.HasPrefix(fallbackID, "youtube_") {
		fallbackID = strings.TrimPrefix(fallbackID, "youtube_")
	}
	if fallbackID == "" {
		return stockplan.YouTubeVideo{}, err
	}
	return stockplan.YouTubeVideo{ID: fallbackID, URL: rawURL}, nil
}

func parseYouTubeURLForDiscovery(raw string) (stockplan.YouTubeVideo, error) {
	return stockplan.ParseYouTubeURL(raw)
}

func candidateBetter(a, b scriptports.VideoSourceCandidate) bool {
	if a.MetadataScore != b.MetadataScore {
		return a.MetadataScore > b.MetadataScore
	}
	return a.Rank < b.Rank
}

var _ scriptports.VideoSourceDiscovery = (*YouTubeSourceDiscoveryAdapter)(nil)
