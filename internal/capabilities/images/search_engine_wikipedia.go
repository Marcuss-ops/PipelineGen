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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/httpjson"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// wikipediaMinRequestInterval is the process-wide pacing floor for the
// Wikipedia action API. Wikimedia throttles anonymous bursts per IP; serializing
// the calls at a rate the API accepts is cheaper than paying a 429 and then a
// cooldown for every scene of the same biography.
const wikipediaMinRequestInterval = 1200 * time.Millisecond

type wikipediaLaneState struct {
	mu            sync.Mutex
	requestMu     sync.Mutex
	lastSearch    time.Time
	cooldownUntil time.Time
}

// wikipediaLane is deliberately package-level, not a field on the service:
// the composition root can construct more than one ImageStorageService, and
// each one used to grant itself an independent burst allowance. Wikimedia
// throttles the IP, not the Go object.
var wikipediaLane wikipediaLaneState

// errWikipediaCoolingDown marks a request that was refused locally because the
// shared Wikipedia lane is in its Retry-After window. It is not a transport
// failure: callers stop querying Wikipedia and let the fallback providers run.
var errWikipediaCoolingDown = errors.New("wikipedia: cooling down after HTTP 429")

// wikipediaRequest serializes the actual HTTP round trip across every service
// instance. Pacing alone only spaces request starts; a slow request or a second
// service could still overlap, and Wikimedia answers the overlap with 429.
func wikipediaRequest[T any](ctx context.Context, s *ImageStorageService, requestURL string, opts *httpjson.Options) (T, error) {
	wikipediaLane.requestMu.Lock()
	defer wikipediaLane.requestMu.Unlock()
	return httpjson.GetJSON[T](ctx, s.client, requestURL, opts)
}

// paceWikipedia reserves the next Wikipedia request slot. It is safe for
// concurrent fan-out goroutines because the mutex is held across the wait;
// callers therefore leave the gate already spaced.
func (s *ImageStorageService) paceWikipedia(ctx context.Context) error {
	wikipediaLane.mu.Lock()
	defer wikipediaLane.mu.Unlock()
	now := time.Now()
	if !wikipediaLane.cooldownUntil.IsZero() && now.Before(wikipediaLane.cooldownUntil) {
		return errWikipediaCoolingDown
	}
	if !wikipediaLane.lastSearch.IsZero() {
		if wait := wikipediaMinRequestInterval - now.Sub(wikipediaLane.lastSearch); wait > 0 {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	wikipediaLane.lastSearch = time.Now()
	return nil
}

// logWikipediaRateLimit opens the shared Wikipedia cooldown when the provider
// answers 429. It never retries inline: an immediate retry is what turns one
// refusal into a storm. Subsequent scenes skip Wikipedia until the Retry-After
// window expires and use Commons/SearXNG instead.
func (s *ImageStorageService) logWikipediaRateLimit(err error) {
	var statusErr *httpjson.StatusError
	if s == nil || !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusTooManyRequests {
		return
	}
	cooldown := statusErr.RetryAfterDuration()
	if cooldown <= 0 {
		cooldown = 60 * time.Second
	}
	wikipediaLane.mu.Lock()
	until := time.Now().Add(cooldown)
	if until.After(wikipediaLane.cooldownUntil) {
		wikipediaLane.cooldownUntil = until
	}
	wikipediaLane.mu.Unlock()
	if s.log != nil {
		s.log.Warn("Wikipedia image search rate limited (HTTP 429); cooling down and deferring to fallbacks",
			zap.Duration("retry_after", statusErr.RetryAfterDuration()), zap.Duration("cooldown", cooldown))
	}
}

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
		if err := s.paceWikipedia(ctx); err != nil {
			return "", ""
		}
		searchURL := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s&format=json&srlimit=5", lang, url.QueryEscape(searchQuery))
		searchPayload, err := wikipediaRequest[struct {
			Query struct {
				Search []struct {
					Title string `json:"title"`
				} `json:"search"`
			} `json:"query"`
		}](ctx, s, searchURL, &httpjson.Options{UserAgent: userAgent})
		if err != nil {
			s.logWikipediaRateLimit(err)
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
	if err := s.paceWikipedia(ctx); err != nil {
		return "", ""
	}
	apiURL := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&prop=pageimages&titles=%s&piprop=original|thumbnail&pithumbsize=1000&format=json&redirects=1", lang, url.QueryEscape(bestTitle))
	payload2, err := wikipediaRequest[struct {
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
	}](ctx, s, apiURL, &httpjson.Options{UserAgent: userAgent})
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
	if err := s.paceWikipedia(ctx); err != nil {
		return "", ""
	}
	apiURL := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&prop=pageimages&titles=%s&piprop=original|thumbnail&pithumbsize=1000&format=json&redirects=1", lang, url.QueryEscape(title))
	payload, err := wikipediaRequest[struct {
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
	}](ctx, s, apiURL, &httpjson.Options{UserAgent: userAgent})
	if err != nil {
		s.logWikipediaRateLimit(err)
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
