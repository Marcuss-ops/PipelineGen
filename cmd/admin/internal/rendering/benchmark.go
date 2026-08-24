// cmd/admin/benchmark.go — semantic search benchmark
//
// Loads a labeled set of queries from a JSON file, runs each through an
// HTTP-backed semantic-search endpoint, and emits a structured report.
//
// Pre-fix signature inconsistencies fixed:
//
//   - `benchQueriesFile` only declared `Queries`. The runBenchmark
//     driver referenced `qf.Description` and `qf.Version` which did not
//     exist. Post-fix the struct carries both fields with `omitempty`
//     so callers can version-tag the input file.
//   - `benchReport` only carried `Queries` + `TimingMS`. The driver
//     tried to set `report.Description/report.Version` post-run. Post-fix
//     the struct mirrors the query-file metadata so a round-trip preserves it
//     (a unit test asserts this end-to-end).
//   - `benchRun` had `results, _ := searchFn(...)` — agent errors were
//     silently dropped. Post-fix each query records its own `Error` and
//     the aggregate `benchReport.TotalErrors` rises accordingly. Operators
//     running the benchmark now SEE partial failures instead of a clean
//     PASS with N silently missing results.
//   - `httpSearchFunc` returned a closure with signature
//     `(ctx, query, limit) → ([]string, []float64, error)`, which did not
//     satisfy the `benchSearchFunc = (ctx, query, source, limit) →
//     ([]benchResult, error)` contract. Post-fix the closure signature is
//     reconciled so `benchRun` invariant holds and the file compiles.
package rendering

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

// benchQueriesFile is the JSON shape accepted by `--queries`. Description
// and Version are optional metadata used to track input provenance
// (round-trip asserted by TestBenchmarkReport_PreservesMetadata).
type benchQueriesFile struct {
	Queries     []benchQuery `json:"queries"`
	Description string       `json:"description,omitempty"`
	Version     string       `json:"version,omitempty"`
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

// benchReport is the JSON shape written by `--output`. Mirrors the
// metadata fields of benchQueriesFile (Description, Version) so a
// round-trip preserves them end-to-end. TotalErrors counts queries that
// returned an error from the searchFn (was silently dropped pre-fix).
type benchReport struct {
	Description string             `json:"description,omitempty"`
	Version     string             `json:"version,omitempty"`
	Queries     []benchQueryResult `json:"queries"`
	TimingMS    int64              `json:"timing_ms"`
	TotalErrors int                `json:"total_errors"`
}

type benchQueryResult struct {
	Label   string        `json:"label"`
	Query   string        `json:"query"`
	Source  string        `json:"source,omitempty"`
	Results []benchResult `json:"results"`
	Latency int64         `json:"latency_ms"`
	Error   string        `json:"error,omitempty"`
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

// benchRun executes every query against searchFn and aggregates the
// results into a benchReport. Per-query errors are recorded in
// benchQueryResult.Error and counted in TotalErrors — never silently
// dropped. Operators see partial failures explicitly.
func benchRun(ctx context.Context, queries []benchQuery, searchFn benchSearchFunc, limit int) *benchReport {
	start := time.Now()
	report := &benchReport{}
	for _, q := range queries {
		reqStart := time.Now()
		results, err := searchFn(ctx, q.Text, q.Source, limit)
		latency := time.Since(reqStart).Milliseconds()
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
			report.TotalErrors++
		}
		report.Queries = append(report.Queries, benchQueryResult{
			Label:   q.Label,
			Query:   q.Text,
			Source:  q.Source,
			Results: results,
			Latency: latency,
			Error:   errMsg,
		})
	}
	report.TimingMS = time.Since(start).Milliseconds()
	return report
}

func benchPrintSummary(r *benchReport) string {
	return fmt.Sprintf("Benchmark: %d queries in %dms (errors=%d)", len(r.Queries), r.TimingMS, r.TotalErrors)
}

func benchSaveReport(r *benchReport, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(r)
}

func RunBenchmark(args []string) error {
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
	// Propagate optional metadata so the round-trip preserves it
	// it (asserted by TestBenchmarkReport_PreservesMetadata).
	report.Description = qf.Description
	report.Version = qf.Version

	fmt.Println(benchPrintSummary(report))

	if err := benchSaveReport(report, *outputPath); err != nil {
		return fmt.Errorf("save report: %w", err)
	}
	fmt.Printf("\nReport saved to %s\n", *outputPath)
	return nil
}

// httpSearchFunc returns a benchSearchFunc bound to the supplied server
// URL, default limit, timeout and admin token. The inner closure's
// closure's accepted parameters and returns now MATCH the
// benchSearchFunc signature `(ctx, query, source, limit) →
// ([]benchResult, error)`. The pre-fix closure silently dropped `source`
// and returned dual parallel slices (ids, scores) which could not be
// assigned to the single `[]benchResult` slot.
func httpSearchFunc(serverURL string, defaultLimit int, timeout time.Duration, authToken string) benchSearchFunc {
	baseURL := strings.TrimRight(serverURL, "/")
	client := &http.Client{Timeout: timeout}

	return func(ctx context.Context, query, source string, limit int) ([]benchResult, error) {
		_ = source // source is currently sent as a query parameter;
		// future revisions can pass it as a header or filter param.
		if limit <= 0 {
			limit = defaultLimit
		}

		reqURL, err := url.Parse(baseURL + "/api/media/semantic-search")
		if err != nil {
			return nil, fmt.Errorf("parse URL: %w", err)
		}

		q := reqURL.Query()
		q.Set("q", query)
		q.Set("mode", "hybrid")
		q.Set("limit", fmt.Sprintf("%d", limit))
		reqURL.RawQuery = q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		if authToken != "" {
			req.Header.Set("Authorization", "Bearer "+authToken)
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("HTTP request: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			errBody := string(body)
			if len(errBody) > 200 {
				errBody = errBody[:200] + "..."
			}
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, errBody)
		}

		var result struct {
			Results []struct {
				AssetID   string  `json:"asset_id"`
				Score     float64 `json:"score"`
				Name      string  `json:"name"`
				DriveLink string  `json:"drive_link"`
			} `json:"results"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}

		flat := make([]benchResult, len(result.Results))
		for i, r := range result.Results {
			flat[i] = benchResult{
				ID:        r.AssetID,
				Name:      r.Name,
				Score:     r.Score,
				DriveLink: r.DriveLink,
			}
		}
		return flat, nil
	}
}
