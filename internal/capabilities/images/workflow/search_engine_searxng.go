// Package images — search_engine_searxng.go contains the SearXNG image
// retrieval backend for the search_queries engines
// (LONG-FILES-DECOMPOSITION-2026-07-06 Band B #3; split 2026-08-07 to
// satisfy the strict per-file LOC cap,
// architecture/policy.yaml#max_lines_per_file_strict).
//
// Owns: searchSearXNGImages, searchSearXNGImagesMany.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	"github.com/Marcuss-ops/PipelineGen/pkg/httpjson"
)

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
