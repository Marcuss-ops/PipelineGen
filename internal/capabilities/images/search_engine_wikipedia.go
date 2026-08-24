// Package images — search_engine_wikipedia.go contains the Wikipedia
// image retrieval backend for the search_queries engines
// (LONG-FILES-DECOMPOSITION-2026-07-06 Band B #3; split 2026-08-07 to
// satisfy the strict per-file LOC cap,
// architecture/policy.yaml#max_lines_per_file_strict).
//
// Owns: searchWikipedia, wikipediaThumbnailByExactTitle.
package images

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/httpjson"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

func (s *ImageStorageService) searchWikipedia(ctx context.Context, query, lang string) (string, string) {
	if imgURL, wikiTitle := s.wikipediaThumbnailByExactTitle(ctx, query, lang); imgURL != "" {
		return imgURL, wikiTitle
	}
	searchQueries := []string{strings.TrimSpace(query)}
	if !looksLikeProperName(query) && !textutil.ContainsCI(query, "pizza") && !textutil.ContainsCI(query, "italia") {
		searchQueries = append(searchQueries, strings.TrimSpace(query+" "+lang))
	}
	bestTitle := ""
	for _, searchQuery := range searchQueries {
		if searchQuery == "" {
			continue
		}
		searchURL := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s&format=json&srlimit=5", lang, url.QueryEscape(searchQuery))
		searchPayload, err := httpjson.GetJSON[struct {
			Query struct {
				Search []struct {
					Title string `json:"title"`
				} `json:"search"`
			} `json:"query"`
		}](ctx, s.client, searchURL, &httpjson.Options{UserAgent: userAgent})
		if err != nil {
			s.log.Error("Wikipedia search request failed", zap.Error(err))
			continue
		}
		bestTitle = selectBestWikiTitle(query, searchPayload.Query.Search)
		if bestTitle != "" {
			s.log.Info("Wikipedia best match found", zap.String("title", bestTitle), zap.String("query", searchQuery))
			break
		}
	}
	if bestTitle == "" {
		s.log.Warn("Wikipedia search returned no results", zap.String("query", query))
		return "", ""
	}
	apiURL := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&prop=pageimages&titles=%s&piprop=original|thumbnail&pithumbsize=1000&format=json&redirects=1", lang, url.QueryEscape(bestTitle))
	payload2, err := httpjson.GetJSON[struct {
		Query struct {
			Pages map[string]struct {
				Original struct {
					Source string `json:"source"`
				} `json:"original"`
				Thumbnail struct {
					Source string `json:"source"`
				} `json:"thumbnail"`
			} `json:"pages"`
		} `json:"query"`
	}](ctx, s.client, apiURL, &httpjson.Options{UserAgent: userAgent})
	if err != nil {
		return "", ""
	}
	for _, page := range payload2.Query.Pages {
		if page.Original.Source != "" {
			return page.Original.Source, bestTitle
		}
		if page.Thumbnail.Source != "" {
			return page.Thumbnail.Source, bestTitle
		}
	}
	return "", ""
}

func (s *ImageStorageService) wikipediaThumbnailByExactTitle(ctx context.Context, title, lang string) (string, string) {
	apiURL := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&prop=pageimages&titles=%s&piprop=original|thumbnail&pithumbsize=1000&format=json&redirects=1", lang, url.QueryEscape(title))
	payload, err := httpjson.GetJSON[struct {
		Query struct {
			Pages map[string]struct {
				Title    string `json:"title"`
				Original struct {
					Source string `json:"source"`
				} `json:"original"`
				Thumbnail struct {
					Source string `json:"source"`
				} `json:"thumbnail"`
			} `json:"pages"`
		} `json:"query"`
	}](ctx, s.client, apiURL, &httpjson.Options{UserAgent: userAgent})
	if err != nil {
		return "", ""
	}
	for _, page := range payload.Query.Pages {
		if page.Original.Source != "" {
			return page.Original.Source, page.Title
		}
		if page.Thumbnail.Source != "" {
			return page.Thumbnail.Source, page.Title
		}
	}
	return "", ""
}
