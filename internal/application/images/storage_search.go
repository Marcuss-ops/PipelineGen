package images

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

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

	s.log.Info("Searching for image on Wikipedia", zap.String("query", finalQuery), zap.String("lang", lang))
	imgURL, wikiTitle2 := s.searchWikipedia(finalQuery, lang)
	source := "wikipedia"
	wikiURL := ""
	if wikiTitle2 != "" {
		wikiURL = fmt.Sprintf("https://%s.wikipedia.org/wiki/%s", lang, strings.ReplaceAll(wikiTitle2, " ", "_"))
	}

	if imgURL == "" {
		s.log.Info("Wikipedia failed, trying SearXNG for images", zap.String("query", query))
		imgURL = s.searchSearXNGImages(ctx, query)
		if imgURL != "" {
			source = "searxng"
		}
	}
	if imgURL == "" {
		s.log.Info("SearXNG failed or skipped, falling back to DuckDuckGo (wide)", zap.String("query", query))
		imgURL = s.searchDDGWide(ctx, query)
		source = "duckduckgo"
	}
	if imgURL == "" {
		return nil, fmt.Errorf("no image found for query: %s", query)
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
		if wikiURL != "" {
			meta["source_page_url"] = wikiURL
		}
		meta["source_name"] = source
		meta["source_query"] = finalQuery
		metaJSON, _ := json.Marshal(meta)
		_ = s.repo.UpdateImageMetadata(ctx, asset.Hash, string(metaJSON))
		asset.MetadataJSON = string(metaJSON)
	}
	return asset, err
}

// ── Web Search ─────────────────────────────────────────────────────────

// SearchWebImage searches for a real image matching the prompt via DuckDuckGo.
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

	req, err := http.NewRequestWithContext(ctx, "GET", imgURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create download request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read image body: %w", err)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("downloaded image is empty")
	}

	s.log.Info("Image downloaded", zap.Int("size_bytes", len(body)), zap.String("url", imgURL))

	filename := extractFilename(imgURL, prompt)
	description := fmt.Sprintf("Web image for: %s", prompt)

	asset, err := s.IngestImage(ctx, slug, "", "", strings.NewReader(string(body)), filename, imgURL, description, tags, false, false)
	if err != nil {
		return nil, fmt.Errorf("ingest image: %w", err)
	}

	meta := make(map[string]any)
	if asset.MetadataJSON != "" && asset.MetadataJSON != "{}" {
		_ = json.Unmarshal([]byte(asset.MetadataJSON), &meta)
	}
	meta["source_image_url"] = imgURL
	meta["source_name"] = "duckduckgo"
	meta["source_query"] = prompt
	metaJSON, _ := json.Marshal(meta)
	asset.MetadataJSON = string(metaJSON)

	s.log.Info("Web image ingested successfully",
		zap.String("slug", slug),
		zap.String("hash", asset.Hash),
		zap.String("path", asset.PathRel),
	)
	return asset, nil
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
