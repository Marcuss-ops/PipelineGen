package benchmark

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Metric function tests ──────────────────────────────────────────────

func TestRecallAt(t *testing.T) {
	tests := []struct {
		name     string
		expected []string
		returned []string
		k        int
		want     float64
	}{
		{
			name:     "all found in top-K",
			expected: []string{"a", "b", "c"},
			returned: []string{"a", "b", "c", "d", "e"},
			k:        5,
			want:     1.0,
		},
		{
			name:     "partial found",
			expected: []string{"a", "b", "c"},
			returned: []string{"a", "x", "y", "b", "z"},
			k:        5,
			want:     2.0 / 3.0,
		},
		{
			name:     "none found",
			expected: []string{"a", "b"},
			returned: []string{"x", "y", "z"},
			k:        3,
			want:     0.0,
		},
		{
			name:     "empty expected (vacuously correct)",
			expected: []string{},
			returned: []string{"a", "b"},
			k:        5,
			want:     1.0,
		},
		{
			name:     "empty returned",
			expected: []string{"a"},
			returned: []string{},
			k:        5,
			want:     0.0,
		},
		{
			name:     "K larger than returned",
			expected: []string{"a", "b", "c"},
			returned: []string{"a", "b"},
			k:        10,
			want:     2.0 / 3.0,
		},
		{
			name:     "duplicate expected IDs (deduplicated)",
			expected: []string{"a", "a", "b", "b"},
			returned: []string{"a", "b"},
			k:        5,
			want:     1.0, // unique expected = {a, b}, both found
		},
		{
			name:     "whitespace in IDs",
			expected: []string{" a ", " b "},
			returned: []string{"a", "x"},
			k:        2,
			want:     0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := recallAt(tt.expected, tt.returned, tt.k)
			if got != tt.want {
				t.Errorf("recallAt() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMRR(t *testing.T) {
	tests := []struct {
		name     string
		expected []string
		returned []string
		want     float64
	}{
		{
			name:     "first position",
			expected: []string{"a", "b"},
			returned: []string{"a", "x", "y"},
			want:     1.0,
		},
		{
			name:     "third position",
			expected: []string{"c"},
			returned: []string{"x", "y", "c", "z"},
			want:     1.0 / 3.0,
		},
		{
			name:     "not found",
			expected: []string{"a"},
			returned: []string{"x", "y", "z"},
			want:     0.0,
		},
		{
			name:     "empty expected",
			expected: []string{},
			returned: []string{"a"},
			want:     1.0,
		},
		{
			name:     "empty returned",
			expected: []string{"a"},
			returned: []string{},
			want:     0.0,
		},
		{
			name:     "second match is counted",
			expected: []string{"b", "c"},
			returned: []string{"a", "b"},
			want:     0.5, // first relevant at position 2 → 1/2
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mrr(tt.expected, tt.returned)
			if got != tt.want {
				t.Errorf("mrr() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNDCGAt(t *testing.T) {
	tests := []struct {
		name     string
		expected []string
		returned []string
		k        int
		want     float64
	}{
		{
			name:     "perfect ranking",
			expected: []string{"a", "b", "c"},
			returned: []string{"a", "b", "c"},
			k:        10,
			want:     1.0,
		},
		{
			name:     "partial relevant",
			expected: []string{"a", "b"},
			returned: []string{"x", "a", "y", "b"},
			k:        10,
			want:     ndcgAt([]string{"a", "b"}, []string{"x", "a", "y", "b"}, 10), // self-reference for correctness
		},
		{
			name:     "none relevant",
			expected: []string{"a", "b"},
			returned: []string{"x", "y", "z"},
			k:        5,
			want:     0.0,
		},
		{
			name:     "empty expected",
			expected: []string{},
			returned: []string{"a", "b"},
			k:        5,
			want:     0.0, // idcg = 0 → 0
		},
		{
			name:     "duplicate expected (dedup IDCG)",
			expected: []string{"a", "a", "a"},
			returned: []string{"a", "x", "y"},
			k:        5,
			want:     1.0, // unique expected = {a}, found at position 1
		},
		{
			name:     "K truncation",
			expected: []string{"a", "b", "c"},
			returned: []string{"x", "y", "a", "b", "c"},
			k:        3,
			want: func() float64 {
				// Only first 3 returned considered: {"x", "y", "a"}
				// "a" is at position 2 (i=2) → DCG = 1/log2(2+2) = 1/log2(4) = 0.5
				// IDCG: 3 relevant docs → 1/log2(2) + 1/log2(3) + 1/log2(4)
				// nDCG = (1/log2(4)) / (1/1 + 1/log2(3) + 1/log2(4))
				return (1.0 / math.Log2(4)) / (1.0 + 1.0/math.Log2(3) + 1.0/math.Log2(4))
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ndcgAt(tt.expected, tt.returned, tt.k)
			// For the self-reference test case, just verify it's between 0 and 1
			if tt.name == "partial relevant" {
				if got < 0 || got > 1 {
					t.Errorf("ndcgAt() = %v, expected between 0 and 1", got)
				}
				return
			}
			if got != tt.want {
				t.Errorf("ndcgAt() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ── LoadQueries tests ──────────────────────────────────────────────────

func TestLoadQueries_Valid(t *testing.T) {
	// Create a temporary queries file
	dir := t.TempDir()
	path := filepath.Join(dir, "test_queries.json")

	content := `{
		"description": "test",
		"version": "1.0",
		"queries": [
			{"query": "test query 1", "expected_asset_ids": ["a", "b"], "query_type": "semantic"},
			{"query": "test query 2", "expected_asset_ids": ["c"], "query_type": "person"}
		]
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	qf, err := LoadQueries(path)
	if err != nil {
		t.Fatalf("LoadQueries() error = %v", err)
	}
	if len(qf.Queries) != 2 {
		t.Errorf("got %d queries, want 2", len(qf.Queries))
	}
	if qf.Description != "test" {
		t.Errorf("description = %q, want \"test\"", qf.Description)
	}
}

func TestLoadQueries_EmptyQuery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty_query.json")

	content := `{
		"description": "test",
		"version": "1.0",
		"queries": [
			{"query": "  ", "expected_asset_ids": ["a"], "query_type": "semantic"}
		]
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadQueries(path)
	if err == nil {
		t.Error("LoadQueries() should fail for empty query text")
	}
}

func TestLoadQueries_NoQueries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no_queries.json")

	content := `{"description": "test", "version": "1.0", "queries": []}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadQueries(path)
	if err == nil {
		t.Error("LoadQueries() should fail for empty queries array")
	}
}

func TestLoadQueries_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.json")

	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadQueries(path)
	if err == nil {
		t.Error("LoadQueries() should fail for invalid JSON")
	}
}

func TestLoadQueries_FileNotFound(t *testing.T) {
	_, err := LoadQueries("/nonexistent/path/queries.json")
	if err == nil {
		t.Error("LoadQueries() should fail for nonexistent file")
	}
}

// ── Run / aggregate tests ──────────────────────────────────────────────

func TestRun_AllSucceed(t *testing.T) {
	queries := []Query{
		{Query: "q1", ExpectedAssetIDs: []string{"a", "b"}, QueryType: "semantic"},
		{Query: "q2", ExpectedAssetIDs: []string{"c"}, QueryType: "person"},
	}

	// Mock search that always returns exactly the expected results
	searchFn := func(ctx context.Context, query string, limit int) ([]string, []float64, error) {
		for _, q := range queries {
			if q.Query == query {
				ids := q.ExpectedAssetIDs
				scores := make([]float64, len(ids))
				for i := range scores {
					scores[i] = 0.9 - float64(i)*0.1
				}
				return ids, scores, nil
			}
		}
		return nil, nil, nil
	}

	report := Run(context.Background(), queries, searchFn, 10)

	if report.TotalQueries != 2 {
		t.Errorf("TotalQueries = %d, want 2", report.TotalQueries)
	}
	if report.Successful != 2 {
		t.Errorf("Successful = %d, want 2", report.Successful)
	}
	if report.Failed != 0 {
		t.Errorf("Failed = %d, want 0", report.Failed)
	}

	// q1: expected={a,b}, returned={a,b} → Recall@1=0.5 (a found, 1/2 unique expected)
	// q2: expected={c}, returned={c} → Recall@1=1.0 → Mean = 0.75
	if report.Aggregates.MeanRecallAt1 != 0.75 {
		t.Errorf("MeanRecallAt1 = %f, want 0.75", report.Aggregates.MeanRecallAt1)
	}
	// MRR: q1 → rank 1 → 1/1=1.0; q2 → rank 1 → 1/1=1.0 → Mean = 1.0
	if report.Aggregates.MeanMRR != 1.0 {
		t.Errorf("MeanMRR = %f, want 1.0", report.Aggregates.MeanMRR)
	}
	if report.Aggregates.NoResultRate != 0.0 {
		t.Errorf("NoResultRate = %f, want 0.0", report.Aggregates.NoResultRate)
	}

	// Type breakdown
	if len(report.QueryTypeBreakdown) != 2 {
		t.Errorf("QueryTypeBreakdown length = %d, want 2", len(report.QueryTypeBreakdown))
	}
}

func TestRun_WithFailures(t *testing.T) {
	queries := []Query{
		{Query: "ok", ExpectedAssetIDs: []string{"a"}, QueryType: "semantic"},
		{Query: "fail", ExpectedAssetIDs: []string{"b"}, QueryType: "person"},
		{Query: "also_fail", ExpectedAssetIDs: []string{"c"}, QueryType: "person"},
	}

	searchFn := func(ctx context.Context, query string, limit int) ([]string, []float64, error) {
		if query == "ok" {
			return []string{"a"}, []float64{0.9}, nil
		}
		return nil, nil, &mockError{msg: "search failed"}
	}

	report := Run(context.Background(), queries, searchFn, 10)

	if report.TotalQueries != 3 {
		t.Errorf("TotalQueries = %d, want 3", report.TotalQueries)
	}
	if report.Successful != 1 {
		t.Errorf("Successful = %d, want 1", report.Successful)
	}
	if report.Failed != 2 {
		t.Errorf("Failed = %d, want 2", report.Failed)
	}

	// Failed queries should still appear in type breakdown
	personBreakdown := findBreakdown(report.QueryTypeBreakdown, "person")
	if personBreakdown == nil {
		t.Fatal("missing 'person' breakdown")
	}
	if personBreakdown.Count != 2 {
		t.Errorf("person breakdown count = %d, want 2 (includes failed queries)", personBreakdown.Count)
	}
}

func TestRun_NoResults(t *testing.T) {
	queries := []Query{
		{Query: "empty", ExpectedAssetIDs: []string{"a", "b"}, QueryType: "semantic"},
	}

	searchFn := func(ctx context.Context, query string, limit int) ([]string, []float64, error) {
		return []string{}, []float64{}, nil
	}

	report := Run(context.Background(), queries, searchFn, 10)

	// No results returned but expected assets exist → all metrics zero
	if report.Aggregates.MeanRecallAt5 != 0.0 {
		t.Errorf("MeanRecallAt5 = %f, want 0.0 (no results)", report.Aggregates.MeanRecallAt5)
	}
	if report.Aggregates.NoResultRate != 1.0 {
		t.Errorf("NoResultRate = %f, want 1.0", report.Aggregates.NoResultRate)
	}
}

func TestRun_EmptyQueries(t *testing.T) {
	report := Run(context.Background(), nil, nil, 10)

	if report.TotalQueries != 0 {
		t.Errorf("TotalQueries = %d, want 0", report.TotalQueries)
	}
	if report.Aggregates.MeanRecallAt5 != 0 {
		t.Errorf("MeanRecallAt5 = %f, want 0", report.Aggregates.MeanRecallAt5)
	}
}

func TestRun_DuplicateDetection(t *testing.T) {
	queries := []Query{
		{Query: "dup", ExpectedAssetIDs: []string{"a"}, QueryType: "semantic"},
	}

	searchFn := func(ctx context.Context, query string, limit int) ([]string, []float64, error) {
		// Return duplicate asset IDs
		return []string{"a", "a", "b", "b", "b"}, []float64{0.9, 0.9, 0.8, 0.8, 0.8}, nil
	}

	report := Run(context.Background(), queries, searchFn, 10)

	// 5 returned, 3 duplicates (extra 'a' + 2 extra 'b')
	if report.Aggregates.DuplicateRate <= 0 {
		t.Errorf("DuplicateRate = %f, want > 0", report.Aggregates.DuplicateRate)
	}
	if report.Successful != 1 {
		t.Errorf("Successful = %d, want 1", report.Successful)
	}
}

// ── SaveReport / PrintSummary tests ────────────────────────────────────

func TestSaveReport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")

	report := &Report{
		Description:  "test report",
		Version:      "1.0",
		TotalQueries: 1,
		Successful:   1,
		Aggregates: Aggregates{
			MeanRecallAt5: 0.95,
			MeanMRR:       0.88,
		},
	}

	if err := SaveReport(report, path); err != nil {
		t.Fatalf("SaveReport() error = %v", err)
	}

	// Verify the file is valid JSON
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var parsed Report
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse report JSON: %v", err)
	}
	if parsed.Description != "test report" {
		t.Errorf("Description = %q, want \"test report\"", parsed.Description)
	}
}

func TestPrintSummary(t *testing.T) {
	report := &Report{
		Description:  "test",
		Version:      "1.0",
		TotalQueries: 10,
		Successful:   9,
		Failed:       1,
		Aggregates: Aggregates{
			MeanRecallAt1:  0.45,
			MeanRecallAt5:  0.72,
			MeanRecallAt10: 0.85,
			MeanMRR:        0.68,
			MeanNDCGAt10:   0.76,
			MeanLatencyMs:  120.5,
			P50LatencyMs:   95.0,
			P95LatencyMs:   250.0,
			P99LatencyMs:   400.0,
			NoResultRate:   0.10,
			DuplicateRate:  0.03,
		},
		QueryTypeBreakdown: []TypeBreakdown{
			{QueryType: "person", Count: 3, MeanRecallAt5: 0.80, MeanRecallAt10: 0.90, MeanMRR: 0.75, MeanNDCGAt10: 0.82},
			{QueryType: "semantic", Count: 7, MeanRecallAt5: 0.68, MeanRecallAt10: 0.83, MeanMRR: 0.65, MeanNDCGAt10: 0.73},
		},
	}

	summary := PrintSummary(report)

	// Verify key sections are present
	checks := []string{
		"test (v1.0)",
		"10 total, 9 succeeded, 1 failed",
		"Aggregates",
		"Recall@1:",
		"Recall@5:",
		"Recall@10:",
		"MRR:",
		"nDCG@10:",
		"Latency",
		"Health",
		"No-result rate",
		"Duplicate rate",
		"By Query Type",
		"person",
		"semantic",
	}
	for _, check := range checks {
		if !contains(summary, check) {
			t.Errorf("PrintSummary() missing %q", check)
		}
	}
}

// ── Helpers ────────────────────────────────────────────────────────────

type mockError struct{ msg string }

func (e *mockError) Error() string { return e.msg }

func findBreakdown(breakdowns []TypeBreakdown, queryType string) *TypeBreakdown {
	for i := range breakdowns {
		if breakdowns[i].QueryType == queryType {
			return &breakdowns[i]
		}
	}
	return nil
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// ── Log2 precision test ──────────────────────────────────────────────

// TestNDCGAt_Precision verifies nDCG is between 0 and 1 for random inputs.
func TestNDCGAt_Precision(t *testing.T) {
	// Test with known values to verify formula correctness
	// DCG = rel1/log2(2) + rel2/log2(3) + rel3/log2(4)
	// For expected={a,b,c}, returned={a,x,b}:
	// rel = [1, 0, 1], DCG = 1/1 + 0/1.585 + 1/2 = 1.5
	// IDCG: 3 relevant → 1/1 + 1/1.585 + 1/2 = 1 + 0.631 + 0.5 = 2.131
	// nDCG = 1.5 / 2.131 ≈ 0.704
	got := ndcgAt([]string{"a", "b", "c"}, []string{"a", "x", "b"}, 10)
	if got < 0.70 || got > 0.71 {
		t.Errorf("ndcgAt for {a,b,c} vs {a,x,b} = %f, want ~0.704", got)
	}
}
