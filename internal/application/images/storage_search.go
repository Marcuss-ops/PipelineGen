package images

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/retrieved"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// ── Typed image-operation errors (FASE 2.3, July 2026) ───────────────
//
// Each sentinel wraps a specific failure category so callers can
// distinguish transient (retryable) from permanent failures without
// string-matching HTTP status codes.

// ErrImageNotFound is returned when the remote server responds with
// HTTP 404 — the image does not exist at the given URL. NOT retryable.
var ErrImageNotFound = errors.New("image: not found (HTTP 404)")

// ErrImageTransient is the canonical transient-error sentinel for image
// operations (429, 5xx, timeout, connection refused). Implements the
// retry.RetryableError interface (IsRetryable() bool → true) so
// retry.IsTransient recognises it at the typed layer without substring
// matching. Retryable with backoff.
type errImageTransient struct{}

func (e *errImageTransient) Error() string      { return "image: transient error (retryable)" }
func (e *errImageTransient) IsRetryable() bool  { return true }

var ErrImageTransient error = &errImageTransient{}

// ErrImageInvalidResponse is returned when the HTTP response body is
// not a valid image or JSON (malformed, empty, wrong content-type).
// NOT retryable.
var ErrImageInvalidResponse = errors.New("image: invalid or corrupt response")

// ── Search & Download ─────────────────────────────────────────────────

// SearchAndDownload searches for an image locally and via web APIs.
func (s *ImageStorageService) SearchAndDownload(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string) (*asset.ImageAsset, error) {
	slug := textutil.Slugify(subjectSlug)
	if slug == "" {
		slug = textutil.Slugify(query)
	}
	if lang == "" {
		lang = "it"
	}

	qLower := strings.ToLower(query)
	if qLower == "name" || qLower == "titolo" || len(query) < 2 {
		return nil, fmt.Errorf("invalid query term: %s", query)
	}

	subject, err := s.repo.GetSubjectBySlugOrAlias(ctx, slug)
	if err == nil && subject != nil {
		if images, err := s.repo.ListImagesBySubject(ctx, subject.Slug); err == nil && len(images) > 0 {
			s.log.Info("Images found in local database", zap.String("subject", subject.Slug), zap.Int("count", len(images)))
			if len(images) > 1 {
				source := rand.New(rand.NewSource(time.Now().UnixNano()))
				randomIndex := source.Intn(len(images))
				s.log.Info("Picking random image from database", zap.Int("index", randomIndex), zap.Int("total", len(images)))
				return &images[randomIndex], nil
			}
			return &images[0], nil
		}
	}

	if subject == nil {
		subject = &asset.Subject{Slug: slug, DisplayName: displayName}
		_, err := s.repo.CreateSubject(ctx, subject)
		if err != nil {
			s.log.Warn("Ingest: subject might already exist", zap.String("slug", slug))
		}
	}

	key := "search:" + slug + ":" + lang
	result, err, _ := s.dedup.Do(key, func() (interface{}, error) {
		return s.searchAndDownloadInner(ctx, slug, displayName, query, lang, tags, subject)
	})
	if err != nil {
		return nil, err
	}
	if asset, ok := result.(*asset.ImageAsset); ok {
		return asset, nil
	}
	return nil, fmt.Errorf("singleflight: unexpected result type")
}

func (s *ImageStorageService) searchAndDownloadInner(ctx context.Context, slug, displayName, query, lang string, tags []string, subject *asset.Subject) (*asset.ImageAsset, error) {
	s.log.Info("Disambiguating with Wikidata", zap.String("query", query), zap.String("lang", lang))
	wikiTitle, qid, _ := s.searchWikidata(query, lang)
	finalQuery := query
	if wikiTitle != "" {
		finalQuery = wikiTitle
		s.log.Info("Wikidata disambiguation successful", zap.String("original", query), zap.String("resolved", finalQuery), zap.String("qid", qid))
	} else {
		s.log.Warn("Wikidata disambiguation found nothing", zap.String("query", query))
	}

	// Step 8: route the network search through the RetrievalProviderRegistry.
	// Wikidata disambig above is preserved as it feeds the Wikipedia
	// canonical title rather than performing a network round-trip.
	imgURL, source, wikiURL := s.runRetrievalFallback(ctx, finalQuery, lang)
	if imgURL == "" {
		return nil, fmt.Errorf("no image found for query: %s", finalQuery)
	}

	s.log.Info("Downloading image", zap.String("url", imgURL), zap.String("source", source))
	description := fmt.Sprintf("Image for %s found via %s", displayName, source)

	provCtx := context.WithValue(ctx, SourceTypeKey, "retrieved")
	provCtx = context.WithValue(provCtx, RetrieverKey, source)
	provCtx = context.WithValue(provCtx, ImageURLKey, imgURL)
	if wikiURL != "" {
		provCtx = context.WithValue(provCtx, PageURLKey, wikiURL)
	} else {
		provCtx = context.WithValue(provCtx, PageURLKey, imgURL)
	}
	if source == "wikipedia" {
		provCtx = context.WithValue(provCtx, LicenseKey, "CC-BY-SA-4.0")
		provCtx = context.WithValue(provCtx, AuthorKey, "Wikipedia Contributors")
	} else {
		provCtx = context.WithValue(provCtx, LicenseKey, "Unknown")
		provCtx = context.WithValue(provCtx, AuthorKey, "Unknown")
	}

	asset, err := s.downloadAndIngest(provCtx, slug, imgURL, slug, source, finalQuery, description, tags)
	if err == nil && asset != nil {
		meta := make(map[string]any)
		if asset.MetadataJSON != "" && asset.MetadataJSON != "{}" {
			_ = json.Unmarshal([]byte(asset.MetadataJSON), &meta)
		}
		meta["source_image_url"] = imgURL
		// runRetrievalFallback (Step 8) returns the third value as
		// the canonical page URL; we receive it via the local
		// `wikiURL` variable (preserved from the legacy cascade so
		// metadata writes don't churn).
		if wikiURL != "" {
			meta["source_page_url"] = wikiURL
		}
		meta["source_name"] = source
		meta["source_query"] = finalQuery
		metaJSON, marshalErr := json.Marshal(meta)
		if marshalErr != nil {
			s.log.Error("searchAndDownloadInner: failed to marshal metadata", zap.Error(marshalErr))
			return asset, fmt.Errorf("marshal image metadata: %w", marshalErr)
		}
		if updateErr := s.repo.UpdateImageMetadata(ctx, asset.Hash, string(metaJSON)); updateErr != nil {
			s.log.Error("searchAndDownloadInner: UpdateImageMetadata failed", zap.Error(updateErr))
			return asset, fmt.Errorf("update image metadata: %w", updateErr)
		}
		asset.MetadataJSON = string(metaJSON)
	}
	return asset, err
}

// ── Web Search ─────────────────────────────────────────────────────────

// SearchWebImage searches for a real image matching the prompt via DuckDuckGo
// and downloads+ingests it with retry on transient HTTP errors (429, 5xx).
//
// FASE 2.3 (July 2026): uses retry.Do via pkg/retry for transient errors;
// returns typed ErrImageNotFound on 404, ErrImageInvalidResponse on corrupt
// bodies, and ErrImageTransient on 429/5xx/timeout. Binary content passes
// via bytes.NewReader, not string(body).
func (s *ImageStorageService) SearchWebImage(ctx context.Context, prompt, slug string, tags []string) (*asset.ImageAsset, error) {
	if slug == "" {
		slug = textutil.Slugify(prompt)
	}
	s.log.Info("Searching web image", zap.String("prompt", prompt), zap.String("slug", slug))

	imgURL := s.searchDDGWide(ctx, prompt)
	if imgURL == "" {
		return nil, fmt.Errorf("no image found on DuckDuckGo for: %s", prompt)
	}
	s.log.Info("Found image URL on DuckDuckGo", zap.String("url", imgURL))

	// Check context cancellation before attempting download.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Download and ingest with retry on transient errors (429, 5xx, timeout).
	// Uses bytes.NewReader for binary content — no string(body) conversion.
	var asset *asset.ImageAsset
	err := retry.Do(ctx, func() error {
		// Context check inside retry loop: abort immediately if cancelled.
		if ctx.Err() != nil {
			return ctx.Err()
		}

		req, reqErr := http.NewRequestWithContext(ctx, "GET", imgURL, nil)
		if reqErr != nil {
			return fmt.Errorf("%w: create download request: %v", ErrImageTransient, reqErr)
		}
		req.Header.Set("User-Agent", userAgent)

		resp, doErr := s.client.Do(req)
		if doErr != nil {
			return fmt.Errorf("%w: download: %v", ErrImageTransient, doErr)
		}
		defer resp.Body.Close()

		// Classify HTTP status.
		switch {
		case resp.StatusCode == http.StatusNotFound:
			return fmt.Errorf("%w: url=%s", ErrImageNotFound, imgURL)
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			return fmt.Errorf("%w: HTTP %d", ErrImageTransient, resp.StatusCode)
		case resp.StatusCode != http.StatusOK:
			return fmt.Errorf("%w: unexpected HTTP %d", ErrImageInvalidResponse, resp.StatusCode)
		}

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
		if readErr != nil {
			return fmt.Errorf("%w: read body: %v", ErrImageTransient, readErr)
		}
		if len(body) == 0 {
			return fmt.Errorf("%w: downloaded image is empty", ErrImageInvalidResponse)
		}

		s.log.Info("Image downloaded", zap.Int("size_bytes", len(body)), zap.String("url", imgURL))

		filename := extractFilename(imgURL, prompt)
		description := fmt.Sprintf("Web image for: %s", prompt)

		// FASE 2.3: bytes.NewReader — nessuna conversione string(body).
		var ingestErr error
		asset, ingestErr = s.IngestImage(ctx, slug, "", "", bytes.NewReader(body), filename, imgURL, description, tags, false, false)
		if ingestErr != nil {
			return fmt.Errorf("ingest image: %w", ingestErr)
		}
		return nil
	}, retry.Options{
		MaxAttempts:    3,
		InitialBackoff: 500 * time.Millisecond,
		IsRetryable:    retry.IsTransient,
	})
	if err != nil {
		return nil, err
	}

	// Metadata enrichment — surface failure rather than silently ignoring it.
	meta := make(map[string]any)
	if asset.MetadataJSON != "" && asset.MetadataJSON != "{}" {
		_ = json.Unmarshal([]byte(asset.MetadataJSON), &meta)
	}
	meta["source_image_url"] = imgURL
	meta["source_name"] = "duckduckgo"
	meta["source_query"] = prompt
	metaJSON, marshalErr := json.Marshal(meta)
	if marshalErr != nil {
		s.log.Error("SearchWebImage: failed to marshal metadata", zap.Error(marshalErr))
		return asset, fmt.Errorf("marshal image metadata: %w", marshalErr)
	}
	if updateErr := s.repo.UpdateImageMetadata(ctx, asset.Hash, string(metaJSON)); updateErr != nil {
		s.log.Error("SearchWebImage: UpdateImageMetadata failed", zap.Error(updateErr))
		return asset, fmt.Errorf("update image metadata: %w", updateErr)
	}
	asset.MetadataJSON = string(metaJSON)

	s.log.Info("Web image ingested successfully",
		zap.String("slug", slug),
		zap.String("hash", asset.Hash),
		zap.String("path", asset.PathRel),
	)
	return asset, nil
}

// runRetrievalFallback (Step 8) walks the RetrievalProviderRegistry
// in canonical order (Wikipedia → SearXNG → DuckDuckGo → Drive) and
// returns the first non-empty hit. Returns (imgURL, source, pageURL)
// tuples aligned with the legacy inline cascade semantics:
//   - Wikipedia hit → source="wikipedia", pageURL points at the wiki page
//   - SearXNG hit    → source="searxng", pageURL=imgURL
//   - DuckDuckGo hit → source="duckduckgo", pageURL=imgURL
//   - Drive hit      → source="drive", pageURL=imgURL
//
// When the registry is nil (tests that pre-date Step 8), falls
// through to the legacy inline cascade so pre-rewrite behaviour is
// preserved bit-for-bit.
func (s *ImageStorageService) runRetrievalFallback(ctx context.Context, query, lang string) (imgURL, source, pageURL string) {
	if s.retrievalRegistry == nil {
		// ── Legacy fallback path (kept for tests + back-compat) ──
		s.log.Info("Searching for image on Wikipedia", zap.String("query", query), zap.String("lang", lang))
		imgURL, wikiTitle := s.searchWikipedia(query, lang)
		source = "wikipedia"
		if wikiTitle != "" {
			pageURL = fmt.Sprintf("https://%s.wikipedia.org/wiki/%s", lang, strings.ReplaceAll(wikiTitle, " ", "_"))
		}
		if imgURL != "" {
			return imgURL, source, pageURL
		}
		s.log.Info("Wikipedia failed, trying SearXNG for images", zap.String("query", query))
		imgURL = s.searchSearXNGImages(ctx, query)
		if imgURL != "" {
			return imgURL, "searxng", imgURL
		}
		s.log.Info("SearXNG failed or skipped, falling back to DuckDuckGo (wide)", zap.String("query", query))
		imgURL = s.searchDDGWide(ctx, query)
		if imgURL != "" {
			return imgURL, "duckduckgo", imgURL
		}
		return "", "", ""
	}

	// ── Step 8 registry path ──
	results, err := s.retrievalRegistry.SearchAll(ctx, query, retrieved.RetrievalSearchOptions{
		Lang: lang,
	})
	if err != nil {
		s.log.Warn("retrieval registry SearchAll errored — falling back to legacy cascade",
			zap.String("query", query),
			zap.Error(err),
		)
		return "", "", ""
	}
	if len(results) == 0 {
		s.log.Info("retrieval registry exhausted — no provider produced a hit",
			zap.String("query", query),
		)
		return "", "", ""
	}
	hit := results[0]
	imgURL = hit.PreviewURL
	source = string(hit.Provider)
	pageURL = hit.PageURL
	if pageURL == "" {
		pageURL = hit.PreviewURL
	}
	return imgURL, source, pageURL
}

// ── Web Search Helpers ─────────────────────────────────────────────────

func (s *ImageStorageService) searchDDGWide(ctx context.Context, query string) string {
	vqdURL := fmt.Sprintf("https://duckduckgo.com/?q=%s&iax=images&ia=images", url.QueryEscape(query))
	req, _ := http.NewRequest("GET", vqdURL, nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return ""
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	vqd := extractVQD(string(body))
	if vqd == "" {
		return ""
	}
	for attempt := 0; attempt < 5; attempt++ {
		apiURL := fmt.Sprintf("https://duckduckgo.com/i.js?l=en-us&o=json&q=%s&vqd=%s&f=,,,&p=%d",
			url.QueryEscape(query), vqd, attempt)

		// Retry the per-page HTTP call up to 3 times on transient errors.
		var resp *http.Response
		err := retry.Do(ctx, func() error {
			req, reqErr := http.NewRequest("GET", apiURL, nil)
			if reqErr != nil {
				return reqErr
			}
			req.Header.Set("User-Agent", userAgent)
			var doErr error
			resp, doErr = s.client.Do(req)
			return doErr
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
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
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

func (s *ImageStorageService) searchSearXNGImages(ctx context.Context, query string) string {
	if s.cfg == nil || s.cfg.External.SearxngURL == "" {
		s.log.Info("SearXNG not configured, skipping image search")
		return ""
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, 3*time.Second)
	defer probeCancel()
	probeURL := strings.TrimRight(s.cfg.External.SearxngURL, "/") + "/healthz"
	probeReq, _ := http.NewRequestWithContext(probeCtx, "GET", probeURL, nil)
	probeResp, probeErr := s.client.Do(probeReq)
	if probeErr != nil {
		s.log.Warn("SearXNG unreachable, skipping SearXNG search", zap.Error(probeErr))
		return ""
	}
	probeResp.Body.Close()

	searchCtx, searchCancel := context.WithTimeout(ctx, 5*time.Second)
	defer searchCancel()
	s.log.Info("Searching SearXNG for images", zap.String("query", query))
	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("categories", "images")
	reqURL := fmt.Sprintf("%s/search?%s", strings.TrimRight(s.cfg.External.SearxngURL, "/"), params.Encode())
	req, err := http.NewRequestWithContext(searchCtx, "GET", reqURL, nil)
	if err != nil {
		s.log.Error("Failed to create SearXNG request", zap.Error(err))
		return ""
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		s.log.Error("SearXNG request failed", zap.Error(err))
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.log.Warn("SearXNG returned non-200 status", zap.Int("status", resp.StatusCode))
		return ""
	}
	var data struct {
		Results []struct {
			URL          string `json:"url"`
			ImgSrc       string `json:"img_src"`
			Thumbnail    string `json:"thumbnail"`
			ThumbnailSrc string `json:"thumbnail_src"`
			Width        int    `json:"width,omitempty"`
			Height       int    `json:"height,omitempty"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		s.log.Error("Failed to decode SearXNG response", zap.Error(err))
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

func (s *ImageStorageService) searchWikidata(query, lang string) (string, string, string) {
	apiURL := fmt.Sprintf("https://www.wikidata.org/w/api.php?action=wbsearchentities&search=%s&language=%s&format=json&limit=10", url.QueryEscape(query), lang)
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", "", ""
	}
	defer resp.Body.Close()
	var payload struct {
		Search []struct {
			ID          string `json:"id"`
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"search"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil || len(payload.Search) == 0 {
		return "", "", ""
	}
	bestLabel, bestID, bestDescription := selectBestWikidataHit(query, payload.Search)
	if bestID == "" {
		return "", "", ""
	}
	return bestLabel, bestID, bestDescription
}

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
		req, _ := http.NewRequest("GET", searchURL, nil)
		req.Header.Set("User-Agent", userAgent)
		resp, err := s.client.Do(req)
		if err != nil {
			s.log.Error("Wikipedia search request failed", zap.Error(err))
			continue
		}
		var searchPayload struct {
			Query struct {
				Search []struct {
					Title string `json:"title"`
				} `json:"search"`
			} `json:"query"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&searchPayload); err != nil {
			resp.Body.Close()
			s.log.Error("Failed to decode Wikipedia search response", zap.Error(err))
			continue
		}
		resp.Body.Close()
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
	req2, _ := http.NewRequest("GET", apiURL, nil)
	req2.Header.Set("User-Agent", userAgent)
	resp2, err := s.client.Do(req2)
	if err != nil {
		return "", ""
	}
	defer resp2.Body.Close()
	var payload2 struct {
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
	}
	if err := json.NewDecoder(resp2.Body).Decode(&payload2); err != nil {
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
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	var payload struct {
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
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
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
