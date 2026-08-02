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
	"html"
	"net/url"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/httpjson"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── DuckDuckGo ─────────────────────────────────────────────────────────

func (s *ImageStorageService) searchDDGWide(ctx context.Context, query string) string {
	urls := s.searchDDGWideMany(ctx, query, 1)
	if len(urls) == 0 {
		return ""
	}
	return urls[0]
}

type ddgImageResult struct {
	Image     string `json:"image"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Thumbnail string `json:"thumbnail"`
}

func (s *ImageStorageService) searchDDGWideMany(ctx context.Context, query string, limit int) []string {
	if limit <= 0 {
		limit = 10
	}
	vqdURL := fmt.Sprintf("https://duckduckgo.com/?q=%s&iax=images&ia=images", url.QueryEscape(query))
	body, err := httpjson.GetBytes(ctx, s.client, vqdURL, &httpjson.Options{UserAgent: userAgent})
	if err != nil {
		s.log.Warn("DDG vqd extraction failed", zap.Error(err))
		return nil
	}
	vqd := extractVQD(string(body))
	if vqd == "" {
		return nil
	}
	all := make([]ddgImageResult, 0, limit)
	seen := make(map[string]struct{}, limit)
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
				break
			}
			continue
		}
		var payload struct {
			Results []ddgImageResult `json:"results"`
		}
		if err := json.Unmarshal(body, &payload); err != nil || len(payload.Results) == 0 {
			continue
		}
		for _, result := range payload.Results {
			candidate := result.Image
			if candidate == "" {
				candidate = result.Thumbnail
			}
			if !strings.HasPrefix(candidate, "http") {
				continue
			}
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			all = append(all, result)
		}
		if len(all) >= limit {
			break
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		return ddgImageScore(all[i]) > ddgImageScore(all[j])
	})
	out := make([]string, 0, min(limit, len(all)*2))
	seenOutput := make(map[string]struct{}, cap(out))
	for _, result := range all {
		// Keep the full image and its DDG thumbnail adjacent. If a host
		// blocks hotlinking on the original image, acquisition can still
		// use the normal thumbnail returned by the same search result.
		for _, candidate := range []string{result.Image, result.Thumbnail} {
			if !strings.HasPrefix(candidate, "http") {
				continue
			}
			if _, ok := seenOutput[candidate]; ok {
				continue
			}
			seenOutput[candidate] = struct{}{}
			out = append(out, candidate)
			if len(out) >= limit {
				return out
			}
		}
	}
	return out
}

func ddgImageScore(r ddgImageResult) int {
	switch {
	case r.Width >= 1920 && r.Height >= 1080:
		return 100
	case r.Width >= 1280 && r.Height >= 720:
		return 70
	case r.Width >= 800:
		return 40
	default:
		return 10
	}
}

// ── SearXNG ────────────────────────────────────────────────────────────

func (s *ImageStorageService) searchSearXNGImages(ctx context.Context, query string) string {
	results := s.searchSearXNGImagesMany(ctx, query, 1)
	if len(results) == 0 {
		return ""
	}
	return results[0].PreviewURL
}

func (s *ImageStorageService) searchSearXNGImagesMany(ctx context.Context, query string, limit int) []routing.RetrievalSearchResult {
	if s.cfg == nil || s.cfg.External.SearxngURL == "" {
		s.log.Info("SearXNG not configured, skipping image search")
		return nil
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, 3*time.Second)
	defer probeCancel()
	probeURL := strings.TrimRight(s.cfg.External.SearxngURL, "/") + "/healthz"
	if _, probeErr := httpjson.GetBytes(probeCtx, s.client, probeURL, &httpjson.Options{UserAgent: userAgent}); probeErr != nil {
		s.log.Warn("SearXNG unreachable, skipping SearXNG search", zap.Error(probeErr))
		return nil
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
		return nil
	}
	if len(data.Results) == 0 {
		s.log.Warn("SearXNG returned 0 image results")
		return nil
	}
	if limit <= 0 {
		limit = 10
	}
	results := make([]routing.RetrievalSearchResult, 0, min(limit, len(data.Results)))
	seen := make(map[string]struct{}, cap(results))
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
		if _, ok := seen[img]; ok {
			continue
		}
		seen[img] = struct{}{}
		results = append(results, routing.RetrievalSearchResult{
			PreviewURL: img, PageURL: firstNonEmptyImageURL(r.URL, img),
			Width: r.Width, Height: r.Height,
			License: "Unknown", Author: "Unknown",
		})
		if len(results) >= limit {
			break
		}
	}
	return results
}

func firstNonEmptyImageURL(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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

// searchWikimediaCommons is the explicit-license fallback for retrieval.
// Unlike a generic image search result, Commons imageinfo contains the
// license, author and dimensions needed by the VidRush rights gate.
func (s *ImageStorageService) searchWikimediaCommons(ctx context.Context, query string) routing.RetrievalSearchResult {
	query = strings.TrimSpace(query)
	if query == "" {
		return routing.RetrievalSearchResult{}
	}
	if err := s.waitForCommonsRequest(ctx); err != nil {
		if s.log != nil {
			s.log.Warn("Wikimedia Commons request cancelled before rate-limit slot", zap.String("query", query), zap.Error(err))
		}
		return routing.RetrievalSearchResult{}
	}
	searchURL := "https://api.wikimedia.org/core/v1/commons/search/page?q=" + url.QueryEscape(query) + "&limit=8"
	commonsOptions := &httpjson.Options{
		UserAgent: userAgent,
		Headers:   map[string]string{"Api-User-Agent": userAgent},
	}
	var searchPayload commonsRESTSearchPayload
	err := retry.Do(ctx, func() error {
		var err error
		searchPayload, err = httpjson.GetJSON[commonsRESTSearchPayload](ctx, s.client, searchURL, commonsOptions)
		if err == nil {
			return nil
		}
		var statusErr *httpjson.StatusError
		if errors.As(err, &statusErr) && (statusErr.StatusCode == 429 || statusErr.StatusCode >= 500) {
			// Preserve both the provider-transient classification and the
			// typed HTTP envelope. The retry engine uses the latter to honor
			// Retry-After instead of retrying into the same 429 window.
			return fmt.Errorf("%w: commons HTTP %d: %w", ErrImageTransient, statusErr.StatusCode, err)
		}
		return err
	}, retry.Options{
		MaxAttempts:    3,
		InitialBackoff: 350 * time.Millisecond,
		IsRetryable: func(err error) bool {
			// A 429 is provider backpressure, not a query failure. Do not
			// block the whole VidRush job behind a long Retry-After window;
			// SearchAll immediately continues with the next configured
			// retrieval provider. Retry transient 5xx/transport failures.
			var statusErr *httpjson.StatusError
			if errors.As(err, &statusErr) && statusErr.StatusCode == 429 {
				return false
			}
			return retry.IsTransient(err)
		},
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("Wikimedia Commons search failed", zap.String("query", query), zap.Error(err))
		}
		return routing.RetrievalSearchResult{}
	}
	for _, page := range searchPayload.Pages {
		if !strings.HasPrefix(page.Key, "File:") {
			continue
		}
		pageURL := "https://commons.wikimedia.org/wiki/" + url.PathEscape(page.Key)
		licensePayload, licenseErr := httpjson.GetJSON[commonsRESTPagePayload](ctx, s.client,
			"https://api.wikimedia.org/core/v1/commons/page/"+url.PathEscape(page.Key),
			commonsOptions)
		if licenseErr != nil || !commonsLicenseIsExplicit(licensePayload.License.Title) {
			continue
		}
		filePayload, fileErr := httpjson.GetJSON[commonsRESTFilePayload](ctx, s.client,
			"https://api.wikimedia.org/core/v1/commons/file/"+url.PathEscape(page.Key),
			commonsOptions)
		if fileErr != nil {
			continue
		}
		imageURL := firstNonEmptyImageURL(filePayload.Thumbnail.URL, filePayload.Preferred.URL, filePayload.Original.URL)
		if imageURL == "" || strings.HasSuffix(strings.ToLower(strings.Split(imageURL, "?")[0]), ".svg") {
			continue
		}
		width, height := filePayload.Thumbnail.Width, filePayload.Thumbnail.Height
		if width <= 0 || height <= 0 {
			width, height = filePayload.Original.Width, filePayload.Original.Height
		}
		return routing.RetrievalSearchResult{
			Provider: asset.ProviderWikimediaCommons, Origin: asset.ImageOriginRetrieved,
			PreviewURL: imageURL, PageURL: pageURL, Title: page.Title,
			Width: width, Height: height, License: licensePayload.License.Title,
			Author: filePayload.Latest.User.Name,
		}
	}
	if s.log != nil {
		s.log.Info("Wikimedia Commons returned no explicit-license image", zap.String("query", query))
	}
	return routing.RetrievalSearchResult{}
}

type commonsRESTSearchPayload struct {
	Pages []commonsRESTSearchPage `json:"pages"`
}

type commonsRESTSearchPage struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

type commonsRESTPagePayload struct {
	License struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	} `json:"license"`
}

type commonsRESTFilePayload struct {
	Latest struct {
		User struct {
			Name string `json:"name"`
		} `json:"user"`
	} `json:"latest"`
	Preferred commonsRESTImage `json:"preferred"`
	Original  commonsRESTImage `json:"original"`
	Thumbnail commonsRESTImage `json:"thumbnail"`
}

type commonsRESTImage struct {
	MIMEType string `json:"mediatype"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	URL      string `json:"url"`
}

const commonsMinimumRequestInterval = 2 * time.Second

func (s *ImageStorageService) waitForCommonsRequest(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.commonsSearchMu.Lock()
	defer s.commonsSearchMu.Unlock()
	if wait := commonsMinimumRequestInterval - time.Since(s.commonsLastSearch); wait > 0 {
		if err := retry.Sleep(ctx, wait, retry.Options{}); err != nil {
			return err
		}
	}
	s.commonsLastSearch = time.Now()
	return nil
}

type commonsMetadataValue struct {
	Value json.RawMessage `json:"value"`
}

type commonsImageInfo struct {
	URL         string                          `json:"url"`
	ThumbURL    string                          `json:"thumburl"`
	Width       int                             `json:"width"`
	Height      int                             `json:"height"`
	MIME        string                          `json:"mime"`
	ExtMetadata map[string]commonsMetadataValue `json:"extmetadata"`
}

type commonsPage struct {
	Title     string             `json:"title"`
	ImageInfo []commonsImageInfo `json:"imageinfo"`
}

func firstCommonsMetadata(values map[string]commonsMetadataValue, keys ...string) string {
	for _, key := range keys {
		if value := commonsMetadataText(values[key].Value); value != "" {
			return value
		}
	}
	return ""
}

func commonsMetadataText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	// Numeric/boolean metadata is valid Commons metadata but is not useful
	// as a license or author. Keep the decoder tolerant without inventing
	// rights evidence from an arbitrary JSON representation.
	return ""
}

func commonsLicenseIsExplicit(license string) bool {
	license = strings.ToLower(strings.TrimSpace(license))
	if license == "" || license == "unknown" || license == "n/a" || license == "copyrighted" {
		return false
	}
	for _, marker := range []string{"cc0", "cc by", "cc-by", "creative commons attribution", "public domain", "pd-", "free art license", "gfdl"} {
		if strings.Contains(license, marker) {
			return true
		}
	}
	return false
}

func stripHTMLMetadata(value string) string {
	value = html.UnescapeString(value)
	for {
		start := strings.IndexByte(value, '<')
		if start < 0 {
			break
		}
		end := strings.IndexByte(value[start:], '>')
		if end < 0 {
			value = value[:start]
			break
		}
		value = value[:start] + value[start+end+1:]
	}
	return strings.TrimSpace(value)
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
