// Package images — search_engine_searxng.go contains the SearXNG image
// retrieval backend for the search_queries engines
// (LONG-FILES-DECOMPOSITION-2026-07-06 Band B #3; split 2026-08-07 to
// satisfy the strict per-file LOC cap,
// architecture/policy.yaml#max_lines_per_file_strict).
//
// Owns: searchSearXNGImages, searchSearXNGImagesMany.
package images

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	retrieved "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/search"
	"github.com/Marcuss-ops/PipelineGen/pkg/httpjson"
	"go.uber.org/zap"
)

func (s *ImageStorageService) searchSearXNGImages(ctx context.Context, query string) string {
	results := s.searchSearXNGImagesMany(ctx, query, 1)
	if len(results) == 0 {
		return ""
	}
	return results[0].PreviewURL
}

func (s *ImageStorageService) searchSearXNGImagesMany(ctx context.Context, query string, limit int) []retrieved.RetrievalSearchResult {
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
	if timeRange := searxImageTimeRange(query); timeRange != "" {
		params.Set("time_range", timeRange)
	}
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
	results := make([]retrieved.RetrievalSearchResult, 0, min(limit, len(data.Results)))
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
		// A curated engine set still leaves stock libraries, wallpaper farms
		// and repost farms in the upstream index. They are never identity
		// evidence, so drop them before they consume the per-query budget.
		if searxImageSourceDenied(r.URL) || searxImageSourceDenied(img) {
			continue
		}
		if _, ok := seen[img]; ok {
			continue
		}
		seen[img] = struct{}{}
		results = append(results, retrieved.RetrievalSearchResult{
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

// searxImageDeniedSourceDomains lists hosts that answer a query with stock
// photography, artwork, wallpaper or reposted material instead of the
// requested subject. The 2026-11-18 PERSON audit found them in the majority of
// the first six positions even after the image engines were curated; a domain
// rule at the provider boundary keeps the per-query budget for real sources.
var searxImageDeniedSourceDomains = []string{
	"pinterest.com", "pinimg.com",
	"artic.edu",
	"openverse.org",
	"deviantart.com",
	"pexels.com", "unsplash.com", "flickr.com", "pixabay.com",
	"alamy.com", "gettyimages.com", "shutterstock.com", "istockphoto.com",
	"dreamstime.com", "depositphotos.com", "123rf.com", "stock.adobe.com",
	"vecteezy.com", "freepik.com", "pngtree.com", "cleanpng.com",
	"wallpapers.com", "wallpaperaccess.com", "wallpaperflare.com",
	"stablediffusionweb.com",
}

// searxImageSourceDenied reports whether rawURL belongs to a denied source.
// The host must match exactly or as a subdomain: "notpinterest.com" is not
// denied by "pinterest.com".
func searxImageSourceDenied(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return false
	}
	for _, domain := range searxImageDeniedSourceDomains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

// searxImageTimeRange requests a recency window only for queries that clearly
// ask for current material. It leaves historical or evergreen image searches
// unchanged; SearXNG applies the range only to engines that support it.
func searxImageTimeRange(query string) string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return ""
	}
	currentYear := strconv.Itoa(time.Now().UTC().Year())
	if strings.Contains(q, currentYear) {
		return "year"
	}
	for _, token := range []string{"latest", "recent", "recenti", "oggi", "attuale", "attuali", "this year", "last 12 months"} {
		if strings.Contains(q, token) {
			return "year"
		}
	}
	return ""
}
