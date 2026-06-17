// Package benchmark provides a retrieval quality benchmark for PipelineGen's
// semantic search. It loads labeled queries from config/benchmark_queries.json,
// runs them against the search service, and computes standard IR metrics:
// Recall@K, MRR, and nDCG@K.
//
// Usage as a standalone binary:
//
//	go run ./cmd/benchmark/ --queries config/benchmark_queries.json --output /tmp/benchmark_report.json
//
// Or as a test:
//
//	go test ./internal/media/realtime/benchmark/ -v -run TestBenchmark
package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"
)

// ── Data types ─────────────────────────────────────────────────────────

// Query represents a single labeled benchmark query.
type Query struct {
	Query            string   `json:"query"`
	ExpectedAssetIDs []string `json:"expected_asset_ids"`
	QueryType        string   `json:"query_type"`
}

// QueriesFile is the root structure of benchmark_queries.json.
type QueriesFile struct {
	Description string  `json:"description"`
	Version     string  `json:"version"`
	Queries     []Query `json:"queries"`
}

// QueryResult holds the search results for a single benchmark query.
type QueryResult struct {
	Query            string    `json:"query"`
	QueryType        string    `json:"query_type"`
	ExpectedAssetIDs []string  `json:"expected_asset_ids"`
	ReturnedAssetIDs []string  `json:"returned_asset_ids"`
	ReturnedScores   []float64 `json:"returned_scores"`
	ResultCount      int       `json:"result_count"`
	RecallAt1        float64   `json:"recall_at_1"`
	RecallAt5        float64   `json:"recall_at_5"`
	RecallAt10       float64   `json:"recall_at_10"`
	MRR              float64   `json:"mrr"`
	NDCGAt10         float64   `json:"ndcg_at_10"`
	DurationMs       int64     `json:"duration_ms"`
	Error            string    `json:"error,omitempty"`
}

// Report is the final benchmark output.
type Report struct {
	Description        string          `json:"description"`
	Version            string          `json:"version"`
	GeneratedAt        string          `json:"generated_at"`
	TotalQueries       int             `json:"total_queries"`
	Successful         int             `json:"successful"`
	Failed             int             `json:"failed"`
	QueryResults       []QueryResult   `json:"query_results"`
	Aggregates         Aggregates      `json:"aggregates"`
	QueryTypeBreakdown []TypeBreakdown `json:"query_type_breakdown"`
}

// Aggregates holds averaged metrics across all queries.
type Aggregates struct {
	MeanRecallAt1  float64 `json:"mean_recall_at_1"`
	MeanRecallAt5  float64 `json:"mean_recall_at_5"`
	MeanRecallAt10 float64 `json:"mean_recall_at_10"`
	MeanMRR        float64 `json:"mean_mrr"`
	MeanNDCGAt10   float64 `json:"mean_ndcg_at_10"`
	MeanLatencyMs  float64 `json:"mean_latency_ms"`
	P50LatencyMs   float64 `json:"p50_latency_ms"`
	P95LatencyMs   float64 `json:"p95_latency_ms"`
	P99LatencyMs   float64 `json:"p99_latency_ms"`
	NoResultRate   float64 `json:"no_result_rate"`
	DuplicateRate  float64 `json:"duplicate_rate"`
}

// TypeBreakdown holds metrics grouped by query_type.
type TypeBreakdown struct {
	QueryType      string  `json:"query_type"`
	Count          int     `json:"count"`
	MeanRecallAt5  float64 `json:"mean_recall_at_5"`
	MeanRecallAt10 float64 `json:"mean_recall_at_10"`
	MeanMRR        float64 `json:"mean_mrr"`
	MeanNDCGAt10   float64 `json:"mean_ndcg_at_10"`
}

// SearchFunc is the interface for the search service.
// Callers provide a function that takes a query and returns ranked asset IDs + scores.
type SearchFunc func(ctx context.Context, query string, limit int) ([]string, []float64, error)

// ── Metric computation ─────────────────────────────────────────────────

// recallAt computes recall@k: proportion of expected results found in top k.
// Deduplicates expected IDs to avoid inflated denominators.
func recallAt(expected []string, returned []string, k int) float64 {
	if len(expected) == 0 {
		return 1.0 // vacuously correct
	}
	expectedSet := make(map[string]bool, len(expected))
	var uniqueExpected []string
	for _, id := range expected {
		id = strings.TrimSpace(id)
		if id != "" && !expectedSet[id] {
			uniqueExpected = append(uniqueExpected, id)
			expectedSet[id] = true
		}
	}
	if len(uniqueExpected) == 0 {
		return 1.0
	}

	limit := k
	if limit > len(returned) {
		limit = len(returned)
	}

	found := 0
	for i := 0; i < limit; i++ {
		if expectedSet[strings.TrimSpace(returned[i])] {
			found++
		}
	}
	return float64(found) / float64(len(uniqueExpected))
}

// mrr computes Mean Reciprocal Rank: 1 / rank of first relevant result.
func mrr(expected []string, returned []string) float64 {
	if len(expected) == 0 {
		return 1.0
	}
	expectedSet := make(map[string]bool, len(expected))
	for _, id := range expected {
		id = strings.TrimSpace(id)
		if id != "" {
			expectedSet[id] = true
		}
	}

	for i, id := range returned {
		if expectedSet[strings.TrimSpace(id)] {
			return 1.0 / float64(i+1)
		}
	}
	return 0.0
}

// ndcgAt computes Normalized Discounted Cumulative Gain at k.
// Uses binary relevance: 1 if in expected, 0 otherwise.
// Deduplicates expected IDs to avoid inflating IDCG.
func ndcgAt(expected []string, returned []string, k int) float64 {
	uniqueExpected := make(map[string]bool, len(expected))
	for _, id := range expected {
		id = strings.TrimSpace(id)
		if id != "" {
			uniqueExpected[id] = true
		}
	}

	limit := k
	if limit > len(returned) {
		limit = len(returned)
	}

	dcg := 0.0
	for i := 0; i < limit; i++ {
		rel := 0.0
		if uniqueExpected[strings.TrimSpace(returned[i])] {
			rel = 1.0
		}
		dcg += rel / math.Log2(float64(i+2))
	}

	idealCount := len(uniqueExpected)
	if idealCount > limit {
		idealCount = limit
	}
	idcg := 0.0
	for i := 0; i < idealCount; i++ {
		idcg += 1.0 / math.Log2(float64(i+2))
	}

	if idcg == 0 {
		return 0.0
	}
	return dcg / idcg
}

// ── Benchmark runner ───────────────────────────────────────────────────

// LoadQueries reads and parses the benchmark queries JSON file.
// Returns an error if the file is invalid or contains empty queries.
func LoadQueries(path string) (*QueriesFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read queries file: %w", err)
	}
	var qf QueriesFile
	if err := json.Unmarshal(data, &qf); err != nil {
		return nil, fmt.Errorf("parse queries file: %w", err)
	}
	if len(qf.Queries) == 0 {
		return nil, fmt.Errorf("no queries found in %s", path)
	}
	for i, q := range qf.Queries {
		if strings.TrimSpace(q.Query) == "" {
			return nil, fmt.Errorf("query %d has empty query text", i)
		}
	}
	return &qf, nil
}

// Run executes all benchmark queries against the provided search function.
// Returns a complete report with per-query metrics and aggregates.
func Run(ctx context.Context, queries []Query, searchFn SearchFunc, searchLimit int) *Report {
	if searchLimit <= 0 {
		searchLimit = 20
	}

	report := &Report{
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		TotalQueries: len(queries),
	}

	var latencies []int64
	var duplicateCount int
	var queryTypeGroups = make(map[string][]QueryResult)

	for _, q := range queries {
		start := time.Now()
		result := QueryResult{
			Query:            q.Query,
			QueryType:        q.QueryType,
			ExpectedAssetIDs: q.ExpectedAssetIDs,
		}

		returnedIDs, returnedScores, err := searchFn(ctx, q.Query, searchLimit)
		duration := time.Since(start).Milliseconds()
		result.DurationMs = duration
		latencies = append(latencies, duration)

		if err != nil {
			result.Error = err.Error()
			report.Failed++
			report.QueryResults = append(report.QueryResults, result)
			queryTypeGroups[q.QueryType] = append(queryTypeGroups[q.QueryType], result)
			continue
		}

		result.ReturnedAssetIDs = returnedIDs
		result.ReturnedScores = returnedScores
		result.ResultCount = len(returnedIDs)

		if len(returnedIDs) == 0 && len(q.ExpectedAssetIDs) > 0 {
			// No-results rate tracking
			result.RecallAt1 = 0
			result.RecallAt5 = 0
			result.RecallAt10 = 0
			result.MRR = 0
			result.NDCGAt10 = 0
		} else {
			result.RecallAt1 = recallAt(q.ExpectedAssetIDs, returnedIDs, 1)
			result.RecallAt5 = recallAt(q.ExpectedAssetIDs, returnedIDs, 5)
			result.RecallAt10 = recallAt(q.ExpectedAssetIDs, returnedIDs, 10)
			result.MRR = mrr(q.ExpectedAssetIDs, returnedIDs)
			result.NDCGAt10 = ndcgAt(q.ExpectedAssetIDs, returnedIDs, 10)

			// Duplicate detection: same asset ID appearing multiple times
			seen := make(map[string]bool)
			for _, id := range returnedIDs {
				if seen[id] {
					duplicateCount++
				}
				seen[id] = true
			}
		}

		report.Successful++
		report.QueryResults = append(report.QueryResults, result)
		queryTypeGroups[q.QueryType] = append(queryTypeGroups[q.QueryType], result)
	}

	// Compute aggregates (corrected: totalReturned for duplicate rate)
	var totalReturned int
	for _, r := range report.QueryResults {
		totalReturned += len(r.ReturnedAssetIDs)
	}
	report.Aggregates = computeAggregates(report.QueryResults, latencies, duplicateCount, totalReturned)
	report.QueryTypeBreakdown = computeTypeBreakdown(queryTypeGroups)

	return report
}

func computeAggregates(results []QueryResult, latencies []int64, duplicateCount, totalReturned int) Aggregates {
	if len(results) == 0 {
		return Aggregates{}
	}

	var sumR1, sumR5, sumR10, sumMRR, sumNDCG, sumLat float64
	noResult := 0
	for _, r := range results {
		sumR1 += r.RecallAt1
		sumR5 += r.RecallAt5
		sumR10 += r.RecallAt10
		sumMRR += r.MRR
		sumNDCG += r.NDCGAt10
		sumLat += float64(r.DurationMs)
		if r.ResultCount == 0 && len(r.ExpectedAssetIDs) > 0 {
			noResult++
		}
	}
	n := float64(len(results))

	// Percentiles
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := percentile(latencies, 0.50)
	p95 := percentile(latencies, 0.95)
	p99 := percentile(latencies, 0.99)

	return Aggregates{
		MeanRecallAt1:  sumR1 / n,
		MeanRecallAt5:  sumR5 / n,
		MeanRecallAt10: sumR10 / n,
		MeanMRR:        sumMRR / n,
		MeanNDCGAt10:   sumNDCG / n,
		MeanLatencyMs:  sumLat / n,
		P50LatencyMs:   p50,
		P95LatencyMs:   p95,
		P99LatencyMs:   p99,
		NoResultRate:   float64(noResult) / n,
		DuplicateRate:  float64(duplicateCount) / float64(max(totalReturned, 1)),
	}
}

func percentile(sorted []int64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Round(p * float64(len(sorted)-1)))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return float64(sorted[idx])
}

func computeTypeBreakdown(groups map[string][]QueryResult) []TypeBreakdown {
	var breakdown []TypeBreakdown
	for qtype, results := range groups {
		if len(results) == 0 {
			continue
		}
		var sumR5, sumR10, sumMRR, sumNDCG float64
		for _, r := range results {
			sumR5 += r.RecallAt5
			sumR10 += r.RecallAt10
			sumMRR += r.MRR
			sumNDCG += r.NDCGAt10
		}
		n := float64(len(results))
		breakdown = append(breakdown, TypeBreakdown{
			QueryType:      qtype,
			Count:          len(results),
			MeanRecallAt5:  sumR5 / n,
			MeanRecallAt10: sumR10 / n,
			MeanMRR:        sumMRR / n,
			MeanNDCGAt10:   sumNDCG / n,
		})
	}
	sort.Slice(breakdown, func(i, j int) bool {
		return breakdown[i].QueryType < breakdown[j].QueryType
	})
	return breakdown
}

// SaveReport writes the benchmark report as formatted JSON.
func SaveReport(report *Report, path string) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// PrintSummary prints a human-readable summary of the benchmark report.
func PrintSummary(report *Report) string {
	a := report.Aggregates
	lines := []string{
		fmt.Sprintf("Benchmark: %s (v%s)", report.Description, report.Version),
		fmt.Sprintf("Queries: %d total, %d succeeded, %d failed",
			report.TotalQueries, report.Successful, report.Failed),
		"",
		"── Aggregates ──",
		fmt.Sprintf("  Recall@1:   %.4f", a.MeanRecallAt1),
		fmt.Sprintf("  Recall@5:   %.4f", a.MeanRecallAt5),
		fmt.Sprintf("  Recall@10:  %.4f", a.MeanRecallAt10),
		fmt.Sprintf("  MRR:        %.4f", a.MeanMRR),
		fmt.Sprintf("  nDCG@10:    %.4f", a.MeanNDCGAt10),
		"",
		"── Latency ──",
		fmt.Sprintf("  Mean:  %.0f ms", a.MeanLatencyMs),
		fmt.Sprintf("  P50:   %.0f ms", a.P50LatencyMs),
		fmt.Sprintf("  P95:   %.0f ms", a.P95LatencyMs),
		fmt.Sprintf("  P99:   %.0f ms", a.P99LatencyMs),
		"",
		"── Health ──",
		fmt.Sprintf("  No-result rate:  %.2f%%", a.NoResultRate*100),
		fmt.Sprintf("  Duplicate rate:  %.2f%%", a.DuplicateRate*100),
	}

	if len(report.QueryTypeBreakdown) > 0 {
		lines = append(lines, "", "── By Query Type ──")
		for _, bt := range report.QueryTypeBreakdown {
			lines = append(lines, fmt.Sprintf("  %-20s (n=%2d): R@5=%.3f  R@10=%.3f  MRR=%.3f  nDCG=%.3f",
				bt.QueryType, bt.Count, bt.MeanRecallAt5, bt.MeanRecallAt10, bt.MeanMRR, bt.MeanNDCGAt10))
		}
	}

	return strings.Join(lines, "\n")
}
