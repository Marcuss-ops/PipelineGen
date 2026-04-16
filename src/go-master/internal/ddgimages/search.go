// Package ddgimages provides DuckDuckGo image search for entity resolution.
// Uses the actual DDG image search API to get direct image URLs.
package ddgimages

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// ImageSearch finds images via DuckDuckGo image search API.
type ImageSearch struct {
	client   *http.Client
	cache    map[string]string
	cacheMu  sync.RWMutex
	cacheTTL time.Duration
}

// NewImageSearch creates a new image search service with caching.
func NewImageSearch(cacheTTL time.Duration) *ImageSearch {
	if cacheTTL == 0 {
		cacheTTL = 1 * time.Hour
	}
	return &ImageSearch{
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		cache:    make(map[string]string),
		cacheTTL: cacheTTL,
	}
}

// SearchEntityImage searches DuckDuckGo images and returns the FIRST direct image URL found.
func (s *ImageSearch) SearchEntityImage(entity string) string {
	// Check cache
	s.cacheMu.RLock()
	if cached, ok := s.cache[entity]; ok {
		s.cacheMu.RUnlock()
		return cached
	}
	s.cacheMu.RUnlock()

	// Try DDG image search API
	imageURL := s.ddgImageSearch(entity)

	// Cache result (even if empty — avoid repeated failed lookups)
	s.cacheMu.Lock()
	s.cache[entity] = imageURL
	s.cacheMu.Unlock()

	return imageURL
}

// SearchBatch searches images for multiple entities at once.
func (s *ImageSearch) SearchBatch(entities []string) map[string]string {
	result := make(map[string]string)
	for _, entity := range entities {
		result[entity] = s.SearchEntityImage(entity)
	}
	return result
}

// ddgImageSearch uses the DuckDuckGo image search API to get the first direct image URL.
func (s *ImageSearch) ddgImageSearch(query string) string {
	// DDG image search API endpoint
	apiURL := fmt.Sprintf("https://duckduckgo.com/i.js?q=%s", url.QueryEscape(query))

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		logger.Debug("DDG image search: failed to create request", zap.String("query", query), zap.Error(err))
		return ""
	}

	// Set headers to look like a browser
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", "https://duckduckgo.com/")

	resp, err := s.client.Do(req)
	if err != nil {
		logger.Debug("DDG image search: request failed", zap.String("query", query), zap.Error(err))
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Debug("DDG image search: non-OK status", zap.String("query", query), zap.Int("status", resp.StatusCode))
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Debug("DDG image search: failed to read response", zap.String("query", query), zap.Error(err))
		return ""
	}

	var result ddgImageResponse
	if err := json.Unmarshal(body, &result); err != nil {
		logger.Debug("DDG image search: failed to parse JSON", zap.String("query", query), zap.Error(err))
		return ""
	}

	// Return the first direct image URL
	if len(result.Results) > 0 {
		img := result.Results[0].Image
		if img != "" {
			logger.Info("DDG image found",
				zap.String("query", query),
				zap.String("image_url", img),
			)
			return img
		}
	}

	// Try fallback: Wikipedia image from Instant Answer
	wikiURL := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_redirect=1", url.QueryEscape(query))
	wikiResp, err := s.client.Get(wikiURL)
	if err == nil {
		defer wikiResp.Body.Close()
		wikiBody, _ := io.ReadAll(wikiResp.Body)
		var wikiResult ddgInstantResponse
		if json.Unmarshal(wikiBody, &wikiResult) == nil && wikiResult.Image != "" {
			logger.Info("Wikipedia image found via fallback",
				zap.String("query", query),
				zap.String("image_url", wikiResult.Image),
			)
			return wikiResult.Image
		}
	}

	logger.Debug("DDG image search: no results", zap.String("query", query))
	return ""
}

// ddgImageResponse represents the DuckDuckGo image search API response.
type ddgImageResponse struct {
	Results []struct {
		Image string `json:"image"`
		Width int    `json:"width"`
		Height int   `json:"height"`
	} `json:"results"`
}

// ddgInstantResponse represents the DuckDuckGo Instant Answer API response (fallback).
type ddgInstantResponse struct {
	Image   string `json:"Image"`
	Heading string `json:"Heading"`
}
