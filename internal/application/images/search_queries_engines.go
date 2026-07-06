// Package images — search_queries_engines.go contains the per-search-engine
// image retrieval backends extracted from search_queries.go
// (LONG-FILES-DECOMPOSITION-2026-07-06 Band B #3).
//
// Owns: searchDDGWide, searchSearXNGImages, searchWikidata, searchWikipedia,
// wikipediaThumbnailByExactTitle.
package images

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/httpjson"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── DuckDuckGo ─────────────────────────────────────────────────────────

func (s *ImageStorageService) searchDDGWide(ctx context.Context, query string) string {
	vqdURL := fmt.Sprintf("https://duckduckgo.com/?q=%s&iax=images&ia=images", url.QueryEscape(query))
	body, err := httpjson.GetBytes(ctx, s.client, vqdURL, &httpjson.Options{UserAgent: userAgent})
	if err != nil {
		s.log.Warn("DDG vqd extraction failed", zap.Error(err))
		return ""
	}
	vqd := extractVQD(string(body))
	if vqd == "" {
		return ""
	}
	for attempt := 0; attempt < 5; attempt++ {
		apiURL := fmt.Sprintf("https://duckduckgo.com/i.js?l=en-us&o=json&q=%s&vqd=%s&f=,,,&p=%d",
			url.QueryEscape(query), vqd, attempt)

		// Retry the per-page HTTP call up to 3 times on transient errors.
		// httpjson.GetBytes wraps transport / status / decode errors;
		// ErrImageTransient shim makes retry.IsTransient recognise the
		// retryable subset byte-stable with the pre-B4 substring-match.
		var body []byte
		err := retry.Do(ctx, func() error {
			gotBody, getErr := httpjson.GetBytes(ctx, s.client, apiURL, &httpjson.Options{UserAgent: userAgent})
			if getErr != nil {
				return fmt.Errorf("%w: %v", ErrImageTransient, getErr)
			}
			body = gotBody
			return nil
		}, retry.Options{
			MaxAttempts:    3,
			InitialBackoff: 200 * time.Millisecond,
			IsRetryable:    retry.IsTransient,
		})
		if err != nil {
			if attempt == 4 {
				return ""
			}
			continue
		}
		var payload struct {
			Results []struct {
				Image     string `json:"image"`
				Width     int    `json:"width"`
				Height    int    `json:"height"`
				Thumbnail string `json:"thumbnail"`
			} `json:"results"`
		}
		if err := json.Unmarshal(body, &payload); err != nil || len(payload.Results) == 0 {
			continue
		}
		best := pickBestImage(payload.Results)
		if best != "" {
			return best
		}
	}
	return ""
}

// ── SearXNG ────────────────────────────────────────────────────────────

func (s *ImageStorageService) searchSearXNGImages(ctx context.Context, query string) string {
	if s.cfg == nil || s.cfg.External.SearxngURL == "" {
		s.log.Info("SearXNG not configured, skipping image search")
		return ""
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, 3*time.Second)
	defer probeCancel()
	probeURL := strings.TrimRight(s.cfg.External.SearxngURL, "/") + "/healthz"
	if _, probeErr := httpjson.GetBytes(probeCtx, s.client, probeURL, &httpjson.Options{UserAgent: userAgent}); probeErr != nil {
		s.log.Warn("SearXNG unreachable, skipping SearXNG search", zap.Error(probeErr))
		return ""
	}

	searchCtx, searchCancel := context.WithTimeout(ctx, 5*time.Second)
	defer searchCancel()
	s.log.Info("Searching SearXNG for images", zap.String("query", query))
	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("categories", "images")
	reqURL := fmt.Sprintf("%s/search?%s", strings.TrimRight(s.cfg.External.SearxngURL, "/"), params.Encode())
	data, err := httpjson.GetJSON[struct {
		Results []struct {
			URL          string `json:"url"`
			ImgSrc       string `json:"img_src"`
			Thumbnail    string `json:"thumbnail"`
			ThumbnailSrc string `json:"thumbnail_src"`
			Width        int    `json:"width,omitempty"`
			Height       int    `json:"height,omitempty"`
		} `json:"results"`
	}](searchCtx, s.client, reqURL, &httpjson.Options{UserAgent: userAgent})
	if err != nil {
		var se *httpjson.StatusError
		if errors.As(err, &se) {
			s.log.Warn("SearXNG returned non-200 status", zap.Int("status", se.StatusCode))
		} else {
			s.log.Error("SearXNG request failed", zap.Error(err))
		}
		return ""
	}
	if len(data.Results) == 0 {
		s.log.Warn("SearXNG returned 0 image results")
		return ""
	}
	best := ""
	bestScore := 0
	for _, r := range data.Results {
		img := r.ImgSrc
		if img == "" {
			img = r.Thumbnail
		}
		if img == "" {
			img = r.ThumbnailSrc
		}
		if img == "" || !strings.HasPrefix(img, "http") {
			continue
		}
		score := 10
		if r.Width >= 1080 {
			score = 100
		} else if r.Width >= 720 {
			score = 70
		} else if r.Width >= 480 {
			score = 40
		}
		if score > bestScore {
			bestScore = score
			best = img
		}
	}
	return best
}

// ── Wikidata ───────────────────────────────────────────────────────────

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

// ── Wikipedia ──────────────────────────────────────────────────────────

func (s *ImageStorageService) searchWikipedia(query, lang string) (string, string) {
	if imgURL, wikiTitle := s.wikipediaThumbnailByExactTitle(query, lang); imgURL != "" {
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
		}](context.Background(), s.client, searchURL, &httpjson.Options{UserAgent: userAgent})
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
	}](context.Background(), s.client, apiURL, &httpjson.Options{UserAgent: userAgent})
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

func (s *ImageStorageService) wikipediaThumbnailByExactTitle(title, lang string) (string, string) {
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
	}](context.Background(), s.client, apiURL, &httpjson.Options{UserAgent: userAgent})
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
