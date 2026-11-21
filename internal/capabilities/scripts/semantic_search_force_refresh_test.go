package scriptgeneration

import (
	"context"
	"strings"
	"testing"
	"time"

	generationcontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/generation"
)

func TestSemanticSearchBatchForceRefreshBypassesPlannerCache(t *testing.T) {
	exact := generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}
	candidate := planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01", exact)
	port := &batchTestPort{results: map[string][]SemanticAssetHit{
		exact.Text: {{AssetID: "asset_42", URL: "https://assets.invalid/42.jpg", SHA256: strings.Repeat("a", 64)}},
	}}
	cache := NewSemanticSearchPlanCache(8)
	planner := batchPlanner(t, port, &batchTestClock{now: time.Unix(100, 0)}, SemanticSearchBatchConfig{
		MaxConcurrency: 1, Cache: cache, CacheTTL: time.Hour,
	})

	plan1, _ := batchPlan(t, candidate)
	if _, err := planner.Run(context.Background(), plan1, "it"); err != nil {
		t.Fatal(err)
	}
	firstCalls := len(port.calls)

	plan2, _ := batchPlan(t, candidate)
	if _, err := planner.Run(context.Background(), plan2, "it"); err != nil {
		t.Fatal(err)
	}
	if len(port.calls) != firstCalls {
		t.Fatalf("warm plan must reuse the cache: calls %d -> %d", firstCalls, len(port.calls))
	}

	plan3, _ := batchPlan(t, candidate)
	if _, err := planner.Run(context.Background(), plan3, "it", true); err != nil {
		t.Fatal(err)
	}
	if len(port.calls) != firstCalls+1 {
		t.Fatalf("force refresh must bypass the cache: calls %d -> %d", firstCalls, len(port.calls))
	}
}
