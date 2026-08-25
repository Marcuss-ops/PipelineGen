// cmd/admin/internal/rendering/benchmark_test.go — locks the bench report
// JSON round-trip and error-propagation contracts for the benchmark
// subcommand. Moved here from cmd/admin/admin_test.go when the benchmark
// implementation was decomposed into internal/rendering (the bench types
// are unexported to this package).
package rendering

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestBenchmarkReport_PreservesMetadata(t *testing.T) {
	original := &benchReport{
		Description: "round-trip metadata canonicalisation test",
		Version:     "v1.2.3",
		Queries: []benchQueryResult{
			{Label: "q1", Query: "trees", Results: []benchResult{{ID: "a", Score: 0.95}}, Latency: 42},
		},
		TimingMS:    42,
		TotalErrors: 0,
	}

	// Marshal → unmarshal round-trip through the JSON shape used by
	// benchSaveReport.
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal benchReport: %v", err)
	}
	// Sanity-check the wire shape carries the optional metadata.
	if !strings.Contains(string(raw), `"description":"round-trip metadata canonicalisation test"`) {
		t.Errorf("marshalled report missing Description field: %s", raw)
	}
	if !strings.Contains(string(raw), `"version":"v1.2.3"`) {
		t.Errorf("marshalled report missing Version field: %s", raw)
	}

	var roundTrip benchReport
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("unmarshal benchReport: %v", err)
	}

	if roundTrip.Description != original.Description {
		t.Errorf("Description: got %q, want %q", roundTrip.Description, original.Description)
	}
	if roundTrip.Version != original.Version {
		t.Errorf("Version: got %q, want %q", roundTrip.Version, original.Version)
	}
	// Sanity-check the wire shape carries the optional metadata.
	if len(roundTrip.Queries) != 1 || roundTrip.Queries[0].Label != "q1" {
		t.Errorf("queries round-trip failed: got %+v, want 1 entry with label q1", roundTrip.Queries)
	}
	if roundTrip.TotalErrors != 0 {
		t.Errorf("TotalErrors: got %d, want 0", roundTrip.TotalErrors)
	}
}

func TestBenchmark_PropagatesSearchErrors(t *testing.T) {
	failingErr := errors.New("simulated upstream semantic-search failure")

	// Search fn that fails for every query. The pre-fix code did
	// `results, _ := searchFn(...)` and silently dropped the error;
	// post-fix the error MUST land on benchQueryResult.Error and bump
	// benchReport.TotalErrors.
	queries := []benchQuery{
		{Label: "ok", Text: "trees", Source: "stock"},
		{Label: "broken", Text: "broken", Source: "stock"},
		{Label: "ok-2", Text: "rivers", Source: "stock"},
	}
	searchFn := func(ctx context.Context, q, source string, limit int) ([]benchResult, error) {
		_ = ctx
		_ = source
		_ = limit
		if q == "broken" {
			return nil, failingErr
		}
		return []benchResult{{ID: q, Score: 0.9}}, nil
	}

	report := benchRun(context.Background(), queries, searchFn, 10)

	if report.TotalErrors != 1 {
		t.Errorf("TotalErrors: got %d, want 1 (one failing query)", report.TotalErrors)
	}
	if len(report.Queries) != 3 {
		t.Fatalf("queries length: got %d, want 3", len(report.Queries))
	}

	for i, q := range report.Queries {
		wantErr := ""
		if queries[i].Text == "broken" {
			wantErr = failingErr.Error()
			if q.Results != nil {
				t.Errorf("query[%d] (%s): expected nil results on failing path, got %+v", i, q.Label, q.Results)
			}
		} else if len(q.Results) != 1 {
			t.Errorf("query[%d] (%s): expected 1 result on happy path, got %+v", i, q.Label, q.Results)
		}
		if q.Error != wantErr {
			t.Errorf("query[%d] (%s) Error: got %q, want %q", i, q.Label, q.Error, wantErr)
		}
	}
}
