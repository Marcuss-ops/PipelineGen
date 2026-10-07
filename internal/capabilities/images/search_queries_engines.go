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
// This file contains cross-engine query helpers.
package images

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// firstNonEmptyImageURL preserves the trim-normalized URL contract while
// delegating candidate selection to the canonical text helper.
func firstNonEmptyImageURL(values ...string) string {
	return strings.TrimSpace(textutil.FirstNonEmpty(values...))
}
