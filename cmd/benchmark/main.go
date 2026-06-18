// Command benchmark runs the retrieval quality benchmark against a running
// PipelineGen server. It loads labeled queries from a JSON file, executes
// them via the semantic search HTTP API, computes IR metrics (Recall@K, MRR,
// nDCG@K), and writes a detailed JSON report.
//
// Usage:
//
//	go run ./cmd/benchmark/ \
//	    --server http://localhost:8080 \
//	    --queries config/benchmark_queries.json \
//	    --output /tmp/benchmark_report.json
//
// If --server is omitted, it defaults to http://localhost:8080.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/media/realtime/benchmark"
)

func main() {
	serverURL := flag.String("server", "http://localhost:8080", "PipelineGen server base URL")
	queriesPath := flag.String("queries", "config/benchmark_queries.json", "Path to labeled queries JSON")
	outputPath := flag.String("output", "/tmp/benchmark_report.json", "Path for JSON report output")
	searchLimit := flag.Int("limit", 20, "Max results per query")
	timeoutSec := flag.Int("timeout", 30, "HTTP request timeout in seconds")
	authToken := flag.String("token", "", "Admin auth token (or set VELOX_ADMIN_TOKEN env var)")
	flag.Parse()

	// Auth token: flag takes precedence, then env var
	token := *authToken
	if token == "" {
		token = os.Getenv("VELOX_ADMIN_TOKEN")
	}

	// Load queries
	qf, err := benchmark.LoadQueries(*queriesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load queries: %v\n", err)
		os.Exit(1)
	}

	// Build HTTP search function
	searchFn := httpSearchFunc(*serverURL, *searchLimit, time.Duration(*timeoutSec)*time.Second, token)

	// Run benchmark
	ctx := context.Background()
	report := benchmark.Run(ctx, qf.Queries, searchFn, *searchLimit)
	report.Description = qf.Description
	report.Version = qf.Version

	// Print summary
	fmt.Println(benchmark.PrintSummary(report))

	// Save report
	if err := benchmark.SaveReport(report, *outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save report: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nReport saved to %s\n", *outputPath)
}

// httpSearchFunc creates a benchmark.SearchFunc that calls the PipelineGen
// semantic search HTTP API (GET /api/media/semantic-search?mode=hybrid).
func httpSearchFunc(serverURL string, defaultLimit int, timeout time.Duration, authToken string) benchmark.SearchFunc {
	baseURL := strings.TrimRight(serverURL, "/")
	client := &http.Client{Timeout: timeout}

	return func(ctx context.Context, query string, limit int) ([]string, []float64, error) {
		if limit <= 0 {
			limit = defaultLimit
		}

		reqURL, err := url.Parse(baseURL + "/api/media/semantic-search")
		if err != nil {
			return nil, nil, fmt.Errorf("parse URL: %w", err)
		}

		q := reqURL.Query()
		q.Set("q", query)
		q.Set("mode", "hybrid")
		q.Set("limit", fmt.Sprintf("%d", limit))
		reqURL.RawQuery = q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
		if err != nil {
			return nil, nil, fmt.Errorf("create request: %w", err)
		}

		if authToken != "" {
			req.Header.Set("Authorization", "Bearer "+authToken)
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, nil, fmt.Errorf("HTTP request: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, nil, fmt.Errorf("read body: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			errBody := string(body)
			if len(errBody) > 200 {
				errBody = errBody[:200] + "..."
			}
			return nil, nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, errBody)
		}

		var result struct {
			Results []struct {
				AssetID string  `json:"asset_id"`
				Score   float64 `json:"score"`
			} `json:"results"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, nil, fmt.Errorf("parse response: %w", err)
		}

		ids := make([]string, len(result.Results))
		scores := make([]float64, len(result.Results))
		for i, r := range result.Results {
			ids[i] = r.AssetID
			scores[i] = r.Score
		}
		return ids, scores, nil
	}
}
