package delivery

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestLiveDuckDuckGoEntityImageCatalogBattery is intentionally opt-in because
// it calls the public DuckDuckGo image endpoint and downloads remote assets.
// Run it with:
//
// IMAGE_CATALOG_LIVE_DDG=1 go test ./internal/application/images -run TestLiveDuckDuckGoEntityImageCatalogBattery -v -count=1
func TestLiveDuckDuckGoEntityImageCatalogBattery(t *testing.T) {
	if os.Getenv("IMAGE_CATALOG_LIVE_DDG") != "1" {
		t.Skip("set IMAGE_CATALOG_LIVE_DDG=1 to run the live DuckDuckGo battery")
	}

	samples := []string{
		"Michael Jordan",
		"Elon Musk",
		"Cristiano Ronaldo",
		"Mike Tyson",
		"Taylor Swift",
		"LeBron James",
		"Michael B Jordan",
		"Tim Cook",
		"Bill Gates",
		"Serena Williams",
	}
	client := &http.Client{Timeout: 20 * time.Second}
	service := &ImageStorageService{client: client, log: zap.NewNop()}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	for _, sample := range samples {
		sample := sample
		t.Run(sample, func(t *testing.T) {
			urls := service.searchDDGWideMany(ctx, sample, 10)
			if len(urls) < 10 {
				t.Fatalf("DuckDuckGo returned %d URLs, want at least 10: %v", len(urls), urls)
			}
			urls = urls[:10]
			if duplicate := duplicateLiveURLs(urls); duplicate != "" {
				t.Fatalf("duplicate URL returned: %s", duplicate)
			}

			results := downloadAndDecodeLiveDDGURLs(ctx, client, urls)
			for _, result := range results {
				t.Logf("url=%s status=%d content_type=%s bytes=%d decoded=%t width=%d height=%d error=%s", result.URL, result.StatusCode, result.ContentType, result.Bytes, result.Decoded, result.Width, result.Height, result.Error)
			}
			var failures []string
			for _, result := range results {
				if result.Error != "" {
					failures = append(failures, fmt.Sprintf("%s: %s", result.URL, result.Error))
				}
			}
			if len(failures) > 0 {
				t.Fatalf("live validation failures (%d/10):\n%s", len(failures), strings.Join(failures, "\n"))
			}
		})
	}
}

type liveDDGDownloadResult struct {
	URL         string
	StatusCode  int
	ContentType string
	Bytes       int
	Decoded     bool
	Width       int
	Height      int
	Error       string
}

func downloadAndDecodeLiveDDGURLs(ctx context.Context, client *http.Client, urls []string) []liveDDGDownloadResult {
	results := make([]liveDDGDownloadResult, len(urls))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, rawURL := range urls {
		wg.Add(1)
		go func(i int, rawURL string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = downloadAndDecodeOneLiveDDGURL(ctx, client, rawURL)
		}(i, rawURL)
	}
	wg.Wait()
	return results
}

func downloadAndDecodeOneLiveDDGURL(ctx context.Context, client *http.Client, rawURL string) liveDDGDownloadResult {
	result := liveDDGDownloadResult{URL: rawURL}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		result.Error = "build request: " + err.Error()
		return result
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		result.Error = "download: " + err.Error()
		return result
	}
	defer resp.Body.Close()
	result.StatusCode = resp.StatusCode
	result.ContentType = resp.Header.Get("Content-Type")
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Error = fmt.Sprintf("unexpected HTTP status %d", resp.StatusCode)
		return result
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024+1))
	if err != nil {
		result.Error = "read body: " + err.Error()
		return result
	}
	result.Bytes = len(body)
	if len(body) > 20*1024*1024 {
		result.Error = "image exceeds 20 MiB limit"
		return result
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		result.Error = "decode: " + err.Error()
		return result
	}
	result.Decoded = true
	result.Width = config.Width
	result.Height = config.Height
	if config.Width <= 0 || config.Height <= 0 {
		result.Error = fmt.Sprintf("invalid dimensions %dx%d", config.Width, config.Height)
	}
	return result
}

func duplicateLiveURLs(urls []string) string {
	seen := make(map[string]struct{}, len(urls))
	for _, rawURL := range urls {
		if _, exists := seen[rawURL]; exists {
			return rawURL
		}
		seen[rawURL] = struct{}{}
	}
	return ""
}
