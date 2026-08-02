package integration_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	webclient "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
)

func TestSearXNGSearch(t *testing.T) {
	if os.Getenv("SEARXNG_INTEGRATION") != "1" {
		t.Skip("set SEARXNG_INTEGRATION=1 to run the live SearXNG integration test")
	}
	baseURL := os.Getenv("SEARXNG_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080"
	}
	engineList := os.Getenv("SEARXNG_ENGINES")
	if engineList == "" {
		engineList = "bing,mwmbl,wiby"
	}

	searcher := webclient.NewWebSearcherWithConfig(webclient.WebSearcherConfig{
		BaseURL: baseURL, MaxResults: 5, Timeout: 30 * time.Second,
		Language: "en", Categories: "general", SafeSearch: 0,
		Engines: strings.Split(engineList, ","),
	})
	queries := []string{
		"Mike Tyson bankruptcy career recovery",
		"Muhammad Ali cultural influence boxing",
		"Sugar Ray Robinson career legacy",
	}
	for _, query := range queries {
		query := query
		t.Run(query, func(t *testing.T) {
			results, err := searcher.Search(context.Background(), query)
			if err != nil {
				t.Fatalf("SearXNG query failed: %v", err)
			}
			if len(results) < 3 {
				t.Fatalf("results=%d, want at least 3", len(results))
			}
			seenURLs := map[string]struct{}{}
			domains := map[string]struct{}{}
			for _, result := range results {
				if strings.TrimSpace(result.Title) == "" || strings.TrimSpace(result.URL) == "" {
					t.Fatalf("invalid result: %#v", result)
				}
				if _, duplicate := seenURLs[result.URL]; duplicate {
					t.Fatalf("duplicate URL: %s", result.URL)
				}
				seenURLs[result.URL] = struct{}{}
				parts := strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(result.URL, "https://"), "http://"), "/", 2)
				domains[parts[0]] = struct{}{}
			}
			if len(domains) < 2 {
				t.Fatalf("domains=%d, want at least 2", len(domains))
			}
		})
	}
}
