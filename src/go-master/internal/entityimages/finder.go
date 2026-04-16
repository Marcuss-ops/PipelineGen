// Package entityimages finds images for named entities using Wikipedia + DuckDuckGo.
package entityimages

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// Finder finds images for named entities.
type Finder struct {
	client   *http.Client
	cache    map[string]string
	cacheMu  sync.RWMutex
}

// New creates a new image finder.
func New() *Finder {
	return &Finder{
		client: &http.Client{Timeout: 10 * time.Second},
		cache:  make(map[string]string),
	}
}

// Find returns the first direct image URL for an entity.
// Tries Wikipedia first (best for people/places), then DuckDuckGo fallback.
func (f *Finder) Find(entity string) string {
	// Check cache
	f.cacheMu.RLock()
	if cached, ok := f.cache[entity]; ok {
		f.cacheMu.RUnlock()
		return cached
	}
	f.cacheMu.RUnlock()

	imageURL := ""

	// 1. Try Wikipedia REST API (best for people, places, organizations)
	imageURL = f.wikipediaImage(entity)

	// 2. Fallback: DuckDuckGo Instant Answer
	if imageURL == "" {
		imageURL = f.ddgInstantImage(entity)
	}

	// 3. Fallback: DDG HTML search
	if imageURL == "" {
		imageURL = f.ddgHTMLSearch(entity)
	}

	// Filter out favicon/ico URLs — not actual images
	if strings.HasSuffix(strings.ToLower(imageURL), ".ico") {
		imageURL = ""
	}

	// Cache result
	f.cacheMu.Lock()
	f.cache[entity] = imageURL
	f.cacheMu.Unlock()

	if imageURL != "" {
		logger.Info("Entity image found",
			zap.String("entity", entity),
			zap.String("image_url", imageURL),
		)
	} else {
		logger.Debug("No image found for entity", zap.String("entity", entity))
	}

	return imageURL
}

// FindBatch finds images for multiple entities.
func (f *Finder) FindBatch(entities []string) map[string]string {
	result := make(map[string]string)
	for _, entity := range entities {
		result[entity] = f.Find(entity)
	}
	return result
}

// wikipediaImage tries to get an image from Wikipedia REST API.
func (f *Finder) wikipediaImage(entity string) string {
	// Clean entity name for Wikipedia URL: spaces → underscores
	title := strings.ReplaceAll(entity, " ", "_")
	apiURL := fmt.Sprintf("https://en.wikipedia.org/api/rest_v1/page/summary/%s", title)

	resp, err := f.client.Get(apiURL)
	if err != nil {
		logger.Debug("Wikipedia API request failed", zap.String("entity", entity), zap.Error(err))
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Debug("Wikipedia API non-OK status",
			zap.String("entity", entity),
			zap.Int("status", resp.StatusCode),
		)
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Debug("Wikipedia API failed to read body", zap.String("entity", entity), zap.Error(err))
		return ""
	}

	var result wikiSummary
	if err := json.Unmarshal(body, &result); err != nil {
		logger.Debug("Wikipedia API failed to parse JSON",
			zap.String("entity", entity),
			zap.Error(err),
		)
		return ""
	}

	// Check if this is actually a Wikipedia article (not a disambiguation)
	if result.Type == "disambiguation" {
		logger.Debug("Wikipedia disambiguation page", zap.String("entity", entity))
		return ""
	}

	if result.Thumbnail != nil && result.Thumbnail.Source != "" {
		return result.Thumbnail.Source
	}

	logger.Debug("Wikipedia API no thumbnail",
		zap.String("entity", entity),
		zap.String("type", result.Type),
	)
	return ""
}

// ddgInstantImage tries to get an image from DuckDuckGo Instant Answer API.
func (f *Finder) ddgInstantImage(entity string) string {
	apiURL := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_redirect=1", url.QueryEscape(entity))

	resp, err := f.client.Get(apiURL)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	var result ddgInstant
	if json.Unmarshal(body, &result) != nil {
		return ""
	}

	if result.Image != "" {
		return normalizeDDGURL(result.Image)
	}

	// Try RelatedTopics icons
	for _, topic := range result.RelatedTopics {
		if topic.Icon.URL != "" {
			imgURL := normalizeDDGURL(topic.Icon.URL)
			// Skip favicon/ico files
			if !strings.HasSuffix(strings.ToLower(imgURL), ".ico") {
				return imgURL
			}
		}
	}

	return ""
}

// normalizeDDGURL converts relative DDG URLs to absolute URLs.
func normalizeDDGURL(imgURL string) string {
	if strings.HasPrefix(imgURL, "//") {
		return "https:" + imgURL
	}
	if strings.HasPrefix(imgURL, "/i/") || strings.HasPrefix(imgURL, "/") {
		return "https://duckduckgo.com" + imgURL
	}
	return imgURL
}

// ddgHTMLSearch scrapes the DuckDuckGo HTML search page for the first image.
func (f *Finder) ddgHTMLSearch(entity string) string {
	apiURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(entity))

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := f.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	// Find first <img src="..."> that's not a favicon or spacer
	html := string(body)
	// Simple regex-free search: find <img tags
	imgTag := `<img `
	idx := strings.Index(html, imgTag)
	if idx == -1 {
		return ""
	}

	// Find src attribute
	remaining := html[idx:]
	srcIdx := strings.Index(remaining, `src="`)
	if srcIdx == -1 {
		srcIdx = strings.Index(remaining, `src='`)
		if srcIdx == -1 {
			return ""
		}
		srcStart := srcIdx + 5
		endQuote := strings.Index(remaining[srcStart:], `'`)
		if endQuote == -1 {
			return ""
		}
		return remaining[srcStart : srcStart+endQuote]
	}

	srcStart := srcIdx + 5
	endQuote := strings.Index(remaining[srcStart:], `"`)
	if endQuote == -1 {
		return ""
	}

	imgURL := remaining[srcStart : srcStart+endQuote]

	// Skip tiny/pixel images
	if strings.Contains(imgURL, "pixel") || strings.Contains(imgURL, "spacer") || strings.Contains(imgURL, "favicon") {
		return ""
	}

	return imgURL
}

// wikiSummary represents Wikipedia REST API summary response.
type wikiSummary struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Thumbnail   *struct {
		Source string `json:"source"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	} `json:"thumbnail"`
}

// ddgInstant represents DuckDuckGo Instant Answer API response.
type ddgInstant struct {
	Image         string `json:"Image"`
	Heading       string `json:"Heading"`
	Abstract      string `json:"Abstract"`
	RelatedTopics []struct {
		Icon struct {
			URL string `json:"URL"`
		} `json:"Icon"`
	} `json:"RelatedTopics"`
}
