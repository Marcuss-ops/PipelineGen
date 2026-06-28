// topic_search.go — single canonical YouTube search entry point +
// live-search runner forwarder.
//
// Split out of orchestrator.go in Step 4 so each usecase/ file owns exactly
// one responsibility. The topic scorers + TopicSearchResponse /
// TopicSearchResult types are package-local (consumed as
// *youtubesrc.TopicSearchResponse by the adapter).
//
// CPR-CC-6 Phase 2 (June 2026): topic search absorbed into the search
// capability service (Service.TopicSearch, defined in search_topic.go);
// the orchestrator exposes Service.SearchByTopicWithFilter as a
// thin forwarder for the searcher port surfaced by
// internal/application/assets/providers/youtube.
package usecase

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// SearchByTopicWithFilter is the single canonical YouTube search entry
// point at the application-layer boundary. It ranks YouTube search
// results with an optional publishedAfter date filter.
//
// query: non-empty trimmed search string.
// limit: clamps to [1, 50]; defaults to 10 when <= 0.
// sortMode: forwarded verbatim to SearchLive; "" means "no preference".
// publishedAfter: RFC3339 date string (e.g. "2025-01-01T00:00:00Z") or
// "" for no filter. When set, only videos uploaded AFTER this date
// remain in the response.
//
// Returns an explicit error when the search capability is not wired
// (composition root must include SearchRunner + Log in ServiceDeps so
// NewService wires Service.search).
func (s *Service) SearchByTopicWithFilter(ctx context.Context, query string, limit int, sortMode, publishedAfter string) (*TopicSearchResponse, error) {
	if s.search == nil {
		return nil, fmt.Errorf("youtube: search capability not wired (composition root must include SearchRunner + Log in ServiceDeps for NewService to wire the search service)")
	}
	return s.TopicSearch(ctx, query, limit, sortMode, publishedAfter)
}

// SearchLive performs a live YouTube search via the search runner port.
// Phase 1b stub: returns empty results. Full implementation was in
// adapters/ pre-PR5 capability extraction.
func (s *Service) SearchLive(ctx context.Context, query string, limit int, sortMode string) ([]asset.Asset, error) {
	if isUnavailablePort(s.searchRunner) {
		return nil, nil
	}
	// Phase 1c TODO: restore real implementation tracking searchRunner hops.
	_ = ctx
	_ = query
	_ = limit
	_ = sortMode
	return nil, nil
}
