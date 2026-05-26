package images

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/media/models"
)

// SearchWebImage searches for a real image matching the prompt via DuckDuckGo,
// downloads it, ingests it into the pipeline, and returns the asset.
// This is the Google/DuckDuckGo path for high-resolution images.
func (s *Service) SearchWebImage(ctx context.Context, prompt, slug string, tags []string) (*models.ImageAsset, error) {
	if slug == "" {
		slug = Slugify(prompt)
	}

	s.log.Info("Searching web image", zap.String("prompt", prompt), zap.String("slug", slug))

	// 1. Search DuckDuckGo for real images matching the prompt
	imgURL := s.searchDDGWide(prompt)
	if imgURL == "" {
		return nil, fmt.Errorf("no image found on DuckDuckGo for: %s", prompt)
	}

	s.log.Info("Found image URL on DuckDuckGo", zap.String("url", imgURL))

	// 2. Download the image
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

	// Limit body read to 20MB to avoid OOM
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read image body: %w", err)
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("downloaded image is empty")
	}

	s.log.Info("Image downloaded", zap.Int("size_bytes", len(body)), zap.String("url", imgURL))

	// 3. Extract filename from URL
	filename := extractFilename(imgURL, prompt)
	description := fmt.Sprintf("Web image for: %s", prompt)

	// 4. Ingest via the pipeline (dedup by hash built-in)
	asset, err := s.IngestImage(ctx, slug, "", strings.NewReader(string(body)), filename, imgURL, description, tags, false)
	if err != nil {
		return nil, fmt.Errorf("failed to ingest image: %w", err)
	}

	// Force metadata update with web source info
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

// searchDDGWide searches DuckDuckGo with pagination and returns
// the first suitable large image URL.
func (s *Service) searchDDGWide(query string) string {
	// Step 1: Get the vqd token from the main page
	vqdURL := fmt.Sprintf("https://duckduckgo.com/?q=%s&iax=images&ia=images", url.QueryEscape(query))
	req, _ := http.NewRequest("GET", vqdURL, nil)
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return ""
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// Try multiple vqd extraction patterns
	vqd := extractVQD(string(body))
	if vqd == "" {
		return ""
	}

	// Step 2: Fetch images across multiple pages
	for attempt := 0; attempt < 5; attempt++ {
		apiURL := fmt.Sprintf(
			"https://duckduckgo.com/i.js?l=en-us&o=json&q=%s&vqd=%s&f=,,,&p=%d",
			url.QueryEscape(query), vqd, attempt,
		)
		req, _ = http.NewRequest("GET", apiURL, nil)
		req.Header.Set("User-Agent", userAgent)

		resp, err = s.client.Do(req)
		if err != nil {
			if attempt == 4 {
				return ""
			}
			time.Sleep(200 * time.Millisecond)
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

// extractVQD parses the DuckDuckGo vqd token from the page HTML.
func extractVQD(html string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`vqd=['"](\d+(?:-\d+)?)['"]`),
		regexp.MustCompile(`"vqd":"(\d+(?:-\d+)?)"`),
	}
	for _, re := range patterns {
		matches := re.FindStringSubmatch(html)
		if len(matches) >= 2 {
			return matches[1]
		}
	}
	return ""
}

// pickBestImage selects the largest image from DuckDuckGo results.
func pickBestImage(results []struct {
	Image     string `json:"image"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Thumbnail string `json:"thumbnail"`
}) string {
	best := ""
	bestScore := 0
	for _, r := range results {
		img := r.Image
		if img == "" {
			if r.Thumbnail != "" {
				img = r.Thumbnail
			} else {
				continue
			}
		}
		if !strings.HasPrefix(img, "http") {
			continue
		}

		// Score: prefer larger images
		score := 0
		switch {
		case r.Width >= 1920 && r.Height >= 1080:
			score = 100
		case r.Width >= 1280 && r.Height >= 720:
			score = 70
		case r.Width >= 800:
			score = 40
		default:
			score = 10
		}

		if score > bestScore {
			bestScore = score
			best = img
		}
	}
	return best
}

// extractFilename extracts a filename from a URL with fallback.
func extractFilename(imgURL, fallback string) string {
	if idx := strings.LastIndex(imgURL, "/"); idx >= 0 {
		fn := imgURL[idx+1:]
		// Strip query params
		if qidx := strings.Index(fn, "?"); qidx >= 0 {
			fn = fn[:qidx]
		}
		if fn != "" && strings.Contains(fn, ".") {
			return fn
		}
	}
	return slugToFilename(fallback) + ".jpg"
}

func slugToFilename(s string) string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "image"
	}
	if len(s) > 100 {
		s = s[:100]
	}
	return s
}
