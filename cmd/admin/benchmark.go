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

	"github.com/Marcuss-ops/PipelineGen/internal/application/realtime/benchmark"
)

func runBenchmark(args []string) error {
	fs := flag.NewFlagSet("benchmark", flag.ExitOnError)
	serverURL := fs.String("server", "http://localhost:8080", "PipelineGen server base URL")
	queriesPath := fs.String("queries", "config/benchmark_queries.json", "Path to labeled queries JSON")
	outputPath := fs.String("output", "/tmp/benchmark_report.json", "Path for JSON report output")
	searchLimit := fs.Int("limit", 20, "Max results per query")
	timeoutSec := fs.Int("timeout", 30, "HTTP request timeout in seconds")
	authToken := fs.String("token", "", "Admin auth token (or set VELOX_ADMIN_TOKEN env var)")
	fs.Parse(args)

	token := *authToken
	if token == "" {
		token = os.Getenv("VELOX_ADMIN_TOKEN")
	}

	qf, err := benchmark.LoadQueries(*queriesPath)
	if err != nil {
		return fmt.Errorf("load queries: %w", err)
	}

	searchFn := httpSearchFunc(*serverURL, *searchLimit, time.Duration(*timeoutSec)*time.Second, token)

	ctx := context.Background()
	report := benchmark.Run(ctx, qf.Queries, searchFn, *searchLimit)
	report.Description = qf.Description
	report.Version = qf.Version

	fmt.Println(benchmark.PrintSummary(report))

	if err := benchmark.SaveReport(report, *outputPath); err != nil {
		return fmt.Errorf("save report: %w", err)
	}
	fmt.Printf("\nReport saved to %s\n", *outputPath)
	return nil
}

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
