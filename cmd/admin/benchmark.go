package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	// TODO(dir1.6-followup): benchmark package was retired from internal/application.
	// Body still references symbols (LoadQueries/Run/PrintSummary/SaveReport/SearchFunc).
	// Re-wire with internal/application/assets/maintenance importer after benchmark helpers
	// are reintroduced in a follow-up PR.
)

// TODO(dir1-followup): the benchmark package was retired during DIR-1
// (its host package internal/application/assets/benchmark no longer exists).
// Reintroduce benchmark.LoadQueries / Run / PrintSummary / SaveReport /
// SearchFunc under e.g. internal/application/assets/maintenance or
// internal/cli/benchmark once DIR-2 lands. In the meantime the body
// here is stubbed with explicit "return fmt.Errorf(...)" sentinels so
// the cmd/admin binary still compiles, but `pipelinegen benchmark`
// always exits with a non-zero status.
func runBenchmark(args []string) error {
	fs := flag.NewFlagSet("benchmark", flag.ExitOnError)
	_ = fs.String("server", "http://localhost:8080", "PipelineGen server base URL (DISABLED)")
	_ = fs.String("queries", "config/benchmark_queries.json", "Path to labeled queries JSON (DISABLED)")
	_ = fs.String("output", "/tmp/benchmark_report.json", "Path for JSON report output (DISABLED)")
	_ = fs.Int("limit", 20, "Max results per query (DISABLED)")
	_ = fs.Int("timeout", 30, "HTTP request timeout in seconds (DISABLED)")
	_ = fs.String("token", "", "Admin auth token (DISABLED)")
	fs.Parse(args)
	return fmt.Errorf("benchmark subcommand disabled: package internal/application/assets/maintenance benchmark helpers pending DIR-2 (see TODO at top of cmd/admin/benchmark.go)")
}

// httpSearchFunc body kept to preserve the helpers it provided to the
// retired benchmark runner (LoadQueries/Run/etc). Body now unreachable
// because runBenchmark returns early, so the SearchFunc type is no
// longer constructed. The function signature is preserved as a stub
// to minimise churn once the benchmark helper package is reintroduced.
func httpSearchFunc(serverURL string, defaultLimit int, timeout time.Duration, authToken string) func(ctx context.Context, query string, limit int) ([]string, []float64, error) {
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
