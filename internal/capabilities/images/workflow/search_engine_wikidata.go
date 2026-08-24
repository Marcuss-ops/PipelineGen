// Package images — search_engine_wikidata.go contains the Wikidata entity
// lookup backend for the search_queries engines
// (LONG-FILES-DECOMPOSITION-2026-07-06 Band B #3; split 2026-08-07 to
// satisfy the strict per-file LOC cap,
// architecture/policy.yaml#max_lines_per_file_strict).
//
// Owns: searchWikidata.
package workflow

import (
	"context"
	"fmt"
	"net/url"

	"github.com/Marcuss-ops/PipelineGen/pkg/httpjson"
)

func (s *ImageStorageService) searchWikidata(query, lang string) (string, string, string) {
	apiURL := fmt.Sprintf("https://www.wikidata.org/w/api.php?action=wbsearchentities&search=%s&language=%s&format=json&limit=10", url.QueryEscape(query), lang)
	payload, err := httpjson.GetJSON[struct {
		Search []struct {
			ID          string `json:"id"`
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"search"`
	}](context.Background(), s.client, apiURL, &httpjson.Options{UserAgent: userAgent})
	if err != nil {
		return "", "", ""
	}
	if len(payload.Search) == 0 {
		return "", "", ""
	}
	bestLabel, bestID, bestDescription := selectBestWikidataHit(query, payload.Search)
	if bestID == "" {
		return "", "", ""
	}
	return bestLabel, bestID, bestDescription
}
