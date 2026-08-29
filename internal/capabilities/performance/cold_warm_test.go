package performance

import (
	"strings"
	"testing"
)

func TestRenderColdWarmTextIncludesHeaderAndBuckets(t *testing.T) {
	c := ColdWarmComparison{
		Attempts:     5,
		ColdAttempts: 1,
		WarmAttempts: 4,
		Cold: []OperationBucket{
			{Operation: "chronon.render_loop", Runs: 1, AvgElapsedMS: 25000, MinElapsedMS: 25000, MaxElapsedMS: 25000},
			{Operation: "chronon.startup", Runs: 1, AvgElapsedMS: 5000, MinElapsedMS: 5000, MaxElapsedMS: 5000},
		},
		Warm: []OperationBucket{
			{Operation: "chronon.render_loop", Runs: 4, AvgElapsedMS: 11500, MinElapsedMS: 10000, MaxElapsedMS: 13000},
			{Operation: "chronon.startup", Runs: 4, AvgElapsedMS: 850, MinElapsedMS: 700, MaxElapsedMS: 1000},
		},
	}
	out := RenderColdWarmText(c)
	if !strings.Contains(out, "Cold #1 vs Warm #2-5") {
		t.Fatalf("header missing expected title: %q", out)
	}
	if !strings.Contains(out, "attempts: 5 (cold: 1, warm: 4)") {
		t.Fatalf("header missing attempt counts: %q", out)
	}
	for _, want := range []string{
		"chronon.render_loop",
		"chronon.startup",
		"25000",
		"11500",
		"10000",
		"13000",
		"850",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
}

func TestRenderColdWarmTextScopedToJob(t *testing.T) {
	c := ColdWarmComparison{
		JobID:        "job-x",
		Attempts:     2,
		ColdAttempts: 1,
		WarmAttempts: 1,
		Cold:         []OperationBucket{{Operation: "probe", Runs: 1, AvgElapsedMS: 400, MinElapsedMS: 400, MaxElapsedMS: 400}},
		Warm:         []OperationBucket{{Operation: "probe", Runs: 1, AvgElapsedMS: 200, MinElapsedMS: 200, MaxElapsedMS: 200}},
	}
	out := RenderColdWarmText(c)
	if !strings.Contains(out, "scope: job job-x") {
		t.Fatalf("report missing job scope: %q", out)
	}
}

func TestRenderColdWarmTextEmptyReport(t *testing.T) {
	out := RenderColdWarmText(ColdWarmComparison{})
	if !strings.Contains(out, "(no measured operations in scope)") {
		t.Fatalf("empty report should state no operations: %q", out)
	}
}
