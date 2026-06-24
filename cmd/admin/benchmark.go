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

)

// Stub types replacing the removed benchmark package.
type benchQueriesFile struct {
	Queries []benchQuery `json:"queries"`
}
type benchQuery struct {
	Label  string `json:"label"`
	Text   string `json:"text"`
	Source string `json:"source"`
}
type benchSearchFunc func(ctx context.Context, query, source string, limit int) ([]benchResult, error)
type benchResult struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Score     float64 `json:"score"`
	DriveLink string  `json:"drive_link"`
}
type benchReport struct {
	Queries  []benchQueryResult `json:"queries"`
	TimingMS int64              `json:"timing_ms"`
}
type benchQueryResult struct {
	Label   string        `json:"label"`
	Query   string        `json:"query"`
	Results []benchResult `json:"results"`
	Latency int64         `json:"latency_ms"`
}

func benchLoadQueries(path string) (*benchQueriesFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var qf benchQueriesFile
	if err := json.NewDecoder(f).Decode(&qf); err != nil {
		return nil, err
	}
	return &qf, nil
}

func benchRun(ctx context.Context, queries []benchQuery, searchFn benchSearchFunc, limit int) *benchReport {
	start := time.Now()
	report := &benchReport{}
	for _, q := range queries {
		reqStart := time.Now()
		results, _ := searchFn(ctx, q.Text, q.Source, limit)
		report.Queries = append(report.Queries, benchQueryResult{
			Label:   q.Label,
			Query:   q.Text,
			Results: results,
			Latency: time.Since(reqStart).Milliseconds(),
		})
	}
	report.TimingMS = time.Since(start).Milliseconds()
	return report
}

func benchPrintSummary(r *benchReport) string { return fmt.Sprintf("Benchmark: %d queries in %dms", len(r.Queries), r.TimingMS) }
func benchSaveReport(r *benchReport, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(r)
}

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

	qf, err := benchLoadQueries(*queriesPath)
	if err != nil {
		return fmt.Errorf("load queries: %w", err)
	}

	searchFn := httpSearchFunc(*serverURL, *searchLimit, time.Duration(*timeoutSec)*time.Second, token)

	ctx := context.Background()
	report := benchRun(ctx, qf.Queries, searchFn, *searchLimit)
	report.Description = qf.Description
	report.Version = qf.Version

	fmt.Println(benchPrintSummary(report))

	if err := benchSaveReport(report, *outputPath); err != nil {
		return fmt.Errorf("save report: %w", err)
	}
	fmt.Printf("\nReport saved to %s\n", *outputPath)
	return nil
}

func httpSearchFunc(serverURL string, defaultLimit int, timeout time.Duration, authToken string) benchSearchFunc {
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
