// Package images — search_queries_engines.go contains the shared
// utilities for the per-search-engine image retrieval backends
// (LONG-FILES-DECOMPOSITION-2026-07-06 Band B #3).
//
// Each search engine lives in its own file (split 2026-08-07 to satisfy
// the strict per-file LOC cap,
// architecture/policy.yaml#max_lines_per_file_strict):
//   - search_engine_ddg.go:       DuckDuckGo (searchDDGWide*)
//   - search_engine_searxng.go:   SearXNG (searchSearXNGImages*)
//   - search_engine_wikidata.go:  Wikidata (searchWikidata)
//   - search_engine_wikipedia.go: Wikipedia (searchWikipedia,
//     wikipediaThumbnailByExactTitle)
//   - search_engine_commons.go:   Wikimedia Commons (searchWikimediaCommons
//     and the Commons REST types/helpers)
//
// This file owns the cross-engine helper firstNonEmptyImageURL.
package workflow

import "strings"

// firstNonEmptyImageURL returns the first non-empty (trimmed) value.
// Shared by the SearXNG and Wikimedia Commons engines to pick the best
// image URL from a set of progressively-less-preferred candidates
// (original > preferred > thumbnail).
func firstNonEmptyImageURL(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
