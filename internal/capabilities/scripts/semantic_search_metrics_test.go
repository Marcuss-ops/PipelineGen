package scriptgeneration

import "testing"

// TestSummarizeSemanticSearchBatchSeparatesRecallAndVerify pins the operator
// contract: the batch log must attribute latency to provider recall and to the
// pixel/VLM verdict, while cached and route-skipped rows stay out of both
// distributions because no provider work ran.
func TestSummarizeSemanticSearchBatchSeparatesRecallAndVerify(t *testing.T) {
	report := &SemanticSearchBatchReport{Attempts: []SemanticSearchAttemptResult{
		{Status: "hit", LatencyMS: 10, RecallMS: 4, VerifyMS: 6},
		{Status: "miss", LatencyMS: 20, RecallMS: 9, VerifyMS: 11},
		{Status: "hit", Cached: true, LatencyMS: 0, RecallMS: 100, VerifyMS: 50},
		{Status: "skipped", RouteSkip: true, RecallMS: 100, VerifyMS: 50},
	}}
	summarizeSemanticSearchBatch(report)

	metrics := report.Metrics
	if metrics.RecallMS != 13 || metrics.VerifyMS != 17 {
		t.Fatalf("accumulated stage costs: recall=%d verify=%d, want 13/17", metrics.RecallMS, metrics.VerifyMS)
	}
	if metrics.RecallP50MS != 4 || metrics.VerifyP50MS != 6 {
		t.Fatalf("stage percentiles: recall=%d verify=%d, want 4/6", metrics.RecallP50MS, metrics.VerifyP50MS)
	}
	if metrics.Queries != 3 || metrics.CacheHits != 1 || metrics.RouteSkips != 1 {
		t.Fatalf("population accounting: %+v", metrics)
	}
}
